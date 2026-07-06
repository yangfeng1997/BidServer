package ragent

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"project/internal/core/errcode"
	corerpc "project/internal/core/rpc"
	"project/internal/server/routeragent"
)

var frameBufferPool = sync.Pool{
	New: func() any { return make([]byte, 0, 4096) },
}

type Client struct {
	nodeID  uint32
	sock    string
	poster  corerpc.Poster
	onFrame func(routeragent.Frame)

	mu     sync.Mutex
	conn   net.Conn
	core   *corerpc.Core
	sendCh chan outboundFrame
	done   chan struct{}
	once   sync.Once
}

type outboundFrame struct {
	frame   routeragent.Frame
	wire    routeragent.RPCWireHeader
	hasWire bool
}

func NewClient(nodeID uint32, sock string, poster corerpc.Poster, onFrame func(routeragent.Frame)) *Client {
	return &Client{nodeID: nodeID, sock: sock, poster: poster, onFrame: onFrame}
}

func (c *Client) SetCore(core *corerpc.Core) { c.core = core }

func (c *Client) Connect() error {
	if c.nodeID == 0 {
		return fmt.Errorf("ragent node_id is empty")
	}
	if c.sock == "" {
		return fmt.Errorf("routeragent sock path is empty")
	}
	raw, err := net.Dial("unix", c.sock)
	if err != nil {
		return fmt.Errorf("dial routeragent: %w", err)
	}
	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, c.nodeID)
	if err := writeFrame(raw, routeragent.Frame{Type: routeragent.FrameHandshake, Body: body}); err != nil {
		_ = raw.Close()
		return fmt.Errorf("send routeragent handshake: %w", err)
	}
	ack, ackBuf, err := readFrame(raw)
	if err != nil {
		_ = raw.Close()
		return fmt.Errorf("read routeragent handshake ack: %w", err)
	}
	defer releaseFrameBuffer(ackBuf)
	if ack.Type != routeragent.FrameHandshakeAck || len(ack.Body) == 0 || ack.Body[0] == 0 {
		_ = raw.Close()
		return fmt.Errorf("routeragent handshake rejected")
	}

	c.mu.Lock()
	c.conn = raw
	c.sendCh = make(chan outboundFrame, 4096)
	c.done = make(chan struct{})
	c.mu.Unlock()

	go c.writeLoop(raw)
	go c.readLoop(raw)
	return nil
}

func (c *Client) Close() error {
	var err error
	c.once.Do(func() {
		c.mu.Lock()
		if c.done != nil {
			close(c.done)
		}
		if c.conn != nil {
			err = c.conn.Close()
		}
		c.mu.Unlock()
	})
	return err
}

func (c *Client) SendFrame(target corerpc.Target, header corerpc.Header, body []byte) error {
	wire := routeragent.RPCWireHeader{
		SeqID:       header.SeqID,
		ServerType:  header.ServerType,
		RoutingMode: routingModeToWire(target.Mode),
		DeadlineMs:  header.DeadlineMs,
		WaiterID:    header.WaiterID,
		SrcNodeID:   header.SrcNodeID,
		DestNodeID:  header.DestNodeID,
		RoutingKey:  header.RoutingKey,
		Route:       header.Route,
	}
	if wire.ServerType == 0 {
		wire.ServerType = target.ServerType
	}
	if wire.SrcNodeID == 0 {
		wire.SrcNodeID = c.nodeID
	}
	if target.Mode == corerpc.RoutingDirect {
		wire.DestNodeID = target.NodeID
		wire.RoutingKey = strconv.FormatUint(uint64(target.NodeID), 10)
	}
	frameType := routeragent.FrameRpcNotify
	if header.SeqID != 0 {
		frameType = routeragent.FrameRpcRequest
	}
	return c.send(outboundFrame{frame: routeragent.Frame{Type: frameType, Body: body}, wire: wire, hasWire: true})
}

func (c *Client) Send(frame routeragent.Frame) error {
	return c.send(outboundFrame{frame: frame})
}

func (c *Client) SendRPCFrame(frameType routeragent.FrameType, header routeragent.RPCWireHeader, body []byte) error {
	return c.send(outboundFrame{frame: routeragent.Frame{Type: frameType, Body: body}, wire: header, hasWire: true})
}

func (c *Client) send(frame outboundFrame) error {
	c.mu.Lock()
	sendCh := c.sendCh
	done := c.done
	c.mu.Unlock()
	if sendCh == nil || done == nil {
		return fmt.Errorf("routeragent client is not connected")
	}
	select {
	case <-done:
		return io.EOF
	case sendCh <- frame:
		return nil
	}
}

const clientWriteBatchMaxFrames = 16

func (c *Client) writeLoop(raw net.Conn) {
	buf := make([]byte, 0, 65536)
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.sendCh:
			var err error
			buf, err = appendOutboundFrame(buf[:0], frame)
			if err != nil {
				continue
			}
			for drained := 1; drained < clientWriteBatchMaxFrames; drained++ {
				select {
				case f2 := <-c.sendCh:
					buf, _ = appendOutboundFrame(buf, f2)
				default:
					goto flushNow
				}
			}
		flushNow:
			if _, err := raw.Write(buf); err != nil {
				_ = c.Close()
				return
			}
		}
	}
}

func (c *Client) readLoop(raw net.Conn) {
	for {
		frame, buf, err := readFrame(raw)
		if err != nil {
			_ = c.Close()
			return
		}
		released := false
		release := func() {
			if released {
				return
			}
			released = true
			releaseFrameBuffer(buf)
		}
		switch frame.Type {
		case routeragent.FrameRpcResponse:
			head, err := routeragent.DecodeRPCWireHeader(frame.Header)
			if err != nil || c.core == nil {
				release()
				continue
			}
			c.core.OnResponseWithRelease(head.SeqID, frame.Body, errcode.ErrCode(head.ErrCode), release)
		case routeragent.FrameRpcRequest, routeragent.FrameRpcNotify:
			if c.onFrame != nil && c.poster != nil {
				frame := frame
				c.poster.Post(func() {
					defer release()
					c.onFrame(frame)
				})
			} else {
				release()
			}
		case routeragent.FrameHeartbeat:
			release()
			_ = c.Send(routeragent.Frame{Type: routeragent.FrameHeartbeat})
		default:
			release()
		}
	}
}

func routingModeToWire(mode corerpc.RoutingMode) uint8 {
	switch mode {
	case corerpc.RoutingDirect:
		return uint8(routeragent.RoutingModeDirect)
	case corerpc.RoutingConsistentHash:
		return uint8(routeragent.RoutingModeHash)
	case corerpc.RoutingBroadcast:
		return uint8(routeragent.RoutingModeBroadcast)
	default:
		return uint8(routeragent.RoutingModeAny)
	}
}

func writeFrame(w io.Writer, frame routeragent.Frame) error {
	data, err := routeragent.EncodeFrame(frame)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func appendOutboundFrame(dst []byte, out outboundFrame) ([]byte, error) {
	if !out.hasWire {
		return routeragent.AppendFrame(dst, out.frame)
	}
	headLen, err := routeragent.RPCWireHeaderLen(out.wire)
	if err != nil {
		return nil, err
	}
	if headLen > 0xFFFF {
		return nil, fmt.Errorf("frame header too large: %d", headLen)
	}
	bodyLen := len(out.frame.Body)
	length := 1 + 2 + headLen + bodyLen
	pos := len(dst)
	need := pos + 4 + length
	if cap(dst) < need {
		newCap := cap(dst) * 2
		if newCap < need {
			newCap = need
		}
		grown := make([]byte, need, newCap)
		copy(grown, dst)
		dst = grown
	} else {
		dst = dst[:need]
	}
	binary.BigEndian.PutUint32(dst[pos:pos+4], uint32(length))
	dst[pos+4] = byte(out.frame.Type)
	binary.BigEndian.PutUint16(dst[pos+5:pos+7], uint16(headLen))
	if _, err := routeragent.AppendRPCWireHeader(dst[pos+7:pos+7], out.wire); err != nil {
		return nil, err
	}
	copy(dst[pos+7+headLen:pos+7+headLen+bodyLen], out.frame.Body)
	return dst, nil
}

func readFrame(r io.Reader) (routeragent.Frame, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return routeragent.Frame{}, nil, err
	}
	length := int(binary.BigEndian.Uint32(hdr[:]))
	if length < 3 {
		return routeragent.Frame{}, nil, fmt.Errorf("invalid routeragent frame length %d", length)
	}
	buf := getFrameBuffer(length)
	if _, err := io.ReadFull(r, buf); err != nil {
		releaseFrameBuffer(buf)
		return routeragent.Frame{}, nil, err
	}
	headLen := int(binary.BigEndian.Uint16(buf[1:3]))
	bodyLen := length - 3 - headLen
	if bodyLen < 0 {
		releaseFrameBuffer(buf)
		return routeragent.Frame{}, nil, fmt.Errorf("invalid routeragent frame body length %d", bodyLen)
	}
	return routeragent.Frame{
		Type:   routeragent.FrameType(buf[0]),
		Header: buf[3 : 3+headLen],
		Body:   buf[3+headLen : 3+headLen+bodyLen],
	}, buf, nil
}

func getFrameBuffer(length int) []byte {
	buf := frameBufferPool.Get().([]byte)
	if cap(buf) < length {
		return make([]byte, length)
	}
	return buf[:length]
}

func releaseFrameBuffer(buf []byte) {
	if cap(buf) > 256*1024 {
		return
	}
	frameBufferPool.Put(buf[:0])
}
