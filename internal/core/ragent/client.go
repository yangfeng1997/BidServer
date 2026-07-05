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

type Client struct {
	nodeID  uint32
	sock    string
	poster  corerpc.Poster
	onFrame func(routeragent.Frame)

	mu     sync.Mutex
	conn   net.Conn
	core   *corerpc.Core
	sendCh chan routeragent.Frame
	done   chan struct{}
	once   sync.Once
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
	ack, err := readFrame(raw)
	if err != nil {
		_ = raw.Close()
		return fmt.Errorf("read routeragent handshake ack: %w", err)
	}
	if ack.Type != routeragent.FrameHandshakeAck || len(ack.Body) == 0 || ack.Body[0] == 0 {
		_ = raw.Close()
		return fmt.Errorf("routeragent handshake rejected")
	}

	c.mu.Lock()
	c.conn = raw
	c.sendCh = make(chan routeragent.Frame, 4096)
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
	return c.Send(routeragent.Frame{Type: frameType, Header: routeragent.EncodeRPCWireHeader(wire), Body: body})
}

func (c *Client) Send(frame routeragent.Frame) error {
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

func (c *Client) writeLoop(raw net.Conn) {
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.sendCh:
			if err := writeFrame(raw, frame); err != nil {
				_ = c.Close()
				return
			}
		}
	}
}

func (c *Client) readLoop(raw net.Conn) {
	for {
		frame, err := readFrame(raw)
		if err != nil {
			_ = c.Close()
			return
		}
		switch frame.Type {
		case routeragent.FrameRpcResponse:
			head, err := routeragent.DecodeRPCWireHeader(frame.Header)
			if err != nil || c.core == nil {
				continue
			}
			c.core.OnResponse(head.SeqID, frame.Body, errcode.ErrCode(head.ErrCode))
		case routeragent.FrameRpcRequest, routeragent.FrameRpcNotify:
			if c.onFrame != nil && c.poster != nil {
				frame := frame
				c.poster.Post(func() { c.onFrame(frame) })
			}
		case routeragent.FrameHeartbeat:
			_ = c.Send(routeragent.Frame{Type: routeragent.FrameHeartbeat})
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

func readFrame(r io.Reader) (routeragent.Frame, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return routeragent.Frame{}, err
	}
	length := int(binary.BigEndian.Uint32(hdr))
	if length < 3 {
		return routeragent.Frame{}, fmt.Errorf("invalid routeragent frame length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return routeragent.Frame{}, err
	}
	data := make([]byte, 4+length)
	copy(data[:4], hdr)
	copy(data[4:], buf)
	return routeragent.DecodeFrame(data)
}
