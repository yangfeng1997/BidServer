package main

// 压测示例：
//
// 	go build -o main .
// 	./main --clients 10 --requests 100 --quiet
// 	./main --clients 100 --requests 1000 --quiet --report-every 1s
//
// 单次连通性验证：
//
// 	./main
import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"project/internal/core/codec"
	handlerpb "project/protocol/handler"
)

const (
	cmdPing = 2054
	cmdTong = 2055
)

type config struct {
	addr        string
	text        string
	seq         uint
	timeout     time.Duration
	clients     int
	requests    int
	quiet       bool
	reportEvery time.Duration
	jsonOutput  bool
	csvOutput   bool
}

type failureKind string

const (
	failureConnect         failureKind = "connect"
	failureMarshal         failureKind = "marshal"
	failureEncode          failureKind = "encode"
	failureSend            failureKind = "send"
	failureRead            failureKind = "read"
	failurePacketType      failureKind = "packet_type"
	failureMessageDecode   failureKind = "message_decode"
	failureMessageType     failureKind = "message_type"
	failureSeqMismatch     failureKind = "seq_mismatch"
	failureCmdMismatch     failureKind = "cmd_mismatch"
	failureErrCode         failureKind = "err_code"
	failurePayloadDecode   failureKind = "payload_decode"
	failurePayloadMismatch failureKind = "payload_mismatch"
)

type checkError struct {
	kind failureKind
	err  error
}

func (e *checkError) Error() string { return e.err.Error() }

func (e *checkError) Kind() failureKind { return e.kind }

type benchResult struct {
	sent       atomic.Int64
	succeeded  atomic.Int64
	failed     atomic.Int64
	latMu      sync.Mutex
	latencies  []time.Duration
	failMu     sync.Mutex
	failures   map[failureKind]int64
	reconnects atomic.Int64
}

func main() {
	cfg := parseFlags()
	if cfg.clients <= 1 && cfg.requests <= 1 {
		runOnce(cfg)
		return
	}
	runBench(cfg)
}

func parseFlags() config {
	addr := flag.String("addr", "127.0.0.1:7001", "gatesvr TCP listen address")
	text := flag.String("text", "ping", "ping request text")
	seq := flag.Uint("seq", 1, "client request sequence id for single request mode")
	timeout := flag.Duration("timeout", 3*time.Second, "dial and per-read timeout")
	clients := flag.Int("clients", 1, "concurrent client connections for benchmark mode")
	requests := flag.Int("requests", 1, "requests per client for benchmark mode")
	quiet := flag.Bool("quiet", false, "suppress per-client benchmark errors")
	reportEvery := flag.Duration("report-every", 0, "periodic benchmark progress interval, disabled when 0")
	jsonOutput := flag.Bool("json", false, "print benchmark summary as JSON")
	csvOutput := flag.Bool("csv", false, "print benchmark summary as one CSV row")
	flag.Parse()

	if *seq == 0 || *seq > 0xffff {
		fatalf("seq must be in range 1..65535")
	}
	if *clients <= 0 {
		fatalf("clients must be > 0")
	}
	if *requests <= 0 {
		fatalf("requests must be > 0")
	}
	return config{
		addr:        *addr,
		text:        *text,
		seq:         *seq,
		timeout:     *timeout,
		clients:     *clients,
		requests:    *requests,
		quiet:       *quiet,
		reportEvery: *reportEvery,
		jsonOutput:  *jsonOutput,
		csvOutput:   *csvOutput,
	}
}

func runOnce(cfg config) {
	client, err := newClient(cfg.addr, cfg.timeout)
	if err != nil {
		fatalf("connect: %v", err)
	}
	defer client.close()

	fmt.Printf("handshake ok addr=%s\n", cfg.addr)
	expected := expectedTong(cfg.text)
	rsp, latency, err := client.ping(uint16(cfg.seq), cfg.text, expected, cfg.timeout)
	if err != nil {
		fatalf("ping: %v", err)
	}
	fmt.Printf("ping sent seq=%d cmd=%d text=%q\n", cfg.seq, cmdPing, cfg.text)
	fmt.Printf("tong recv seq=%d cmd=%d text=%q latency=%s\n", cfg.seq, cmdTong, rsp.GetText(), latency)
}

func runBench(cfg config) {
	result := &benchResult{
		latencies: make([]time.Duration, 0, cfg.clients*cfg.requests),
		failures:  make(map[failureKind]int64),
	}
	started := time.Now()
	var wg sync.WaitGroup
	stopReport := make(chan struct{})
	if cfg.reportEvery > 0 {
		go reportProgress(result, started, cfg.reportEvery, stopReport)
	}

	for i := 0; i < cfg.clients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			runBenchClient(cfg, clientID, result)
		}(i)
	}
	wg.Wait()
	close(stopReport)

	elapsed := time.Since(started)
	printSummary(cfg, result, elapsed)
	if result.failed.Load() > 0 {
		os.Exit(1)
	}
}

func runBenchClient(cfg config, clientID int, result *benchResult) {
	client, err := newClient(cfg.addr, cfg.timeout)
	if err != nil {
		result.sent.Add(int64(cfg.requests))
		result.failed.Add(int64(cfg.requests))
		result.addFailure(failureConnect, int64(cfg.requests))
		if !cfg.quiet {
			fmt.Fprintf(os.Stderr, "client=%d connect: %v\n", clientID, err)
		}
		return
	}
	defer client.close()

	for i := 0; i < cfg.requests; i++ {
		seq := uint16((i % 0xffff) + 1)
		text := benchText(cfg.text, clientID, i, seq)
		expected := expectedTong(text)
		result.sent.Add(1)
		_, latency, err := client.ping(seq, text, expected, cfg.timeout)
		if err != nil {
			kind := classifyFailure(err)
			result.failed.Add(1)
			result.addFailure(kind, 1)
			if !cfg.quiet {
				fmt.Fprintf(os.Stderr, "client=%d request=%d seq=%d text=%q: %v\n", clientID, i+1, seq, text, err)
			}
			if shouldReconnect(kind) {
				client.close()
				next, connErr := newClient(cfg.addr, cfg.timeout)
				if connErr != nil {
					client = nil
					remaining := cfg.requests - i - 1
					if remaining > 0 {
						result.sent.Add(int64(remaining))
						result.failed.Add(int64(remaining))
						result.addFailure(failureConnect, int64(remaining))
					}
					if !cfg.quiet {
						fmt.Fprintf(os.Stderr, "client=%d reconnect: %v\n", clientID, connErr)
					}
					return
				}
				result.reconnects.Add(1)
				client = next
			}
			continue
		}
		result.succeeded.Add(1)
		result.latMu.Lock()
		result.latencies = append(result.latencies, latency)
		result.latMu.Unlock()
	}
}

func (r *benchResult) addFailure(kind failureKind, n int64) {
	r.failMu.Lock()
	r.failures[kind] += n
	r.failMu.Unlock()
}

type client struct {
	conn net.Conn
}

func newClient(addr string, timeout time.Duration) (*client, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	c := &client{conn: conn}
	if err := c.writePacket(codec.Packet{Type: codec.PacketHandshake}, timeout); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send handshake: %w", err)
	}
	ack, err := c.readPacket(timeout)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read handshake ack: %w", err)
	}
	if ack.Type != codec.PacketHandshakeAck || len(ack.Body) == 0 || ack.Body[0] == 0 {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake rejected: type=%d body=%x", ack.Type, ack.Body)
	}
	return c, nil
}

func (c *client) close() { _ = c.conn.Close() }

func (c *client) ping(seq uint16, text string, expected string, timeout time.Duration) (*handlerpb.SC_Tong_Rsp, time.Duration, error) {
	reqBody, err := proto.Marshal(&handlerpb.CS_Ping_Req{Text: text})
	if err != nil {
		return nil, 0, check(failureMarshal, "marshal ping: %w", err)
	}
	msgBody, err := codec.EncodeMessage(codec.Message{Type: codec.MessageRequest, SeqID: seq, CmdID: cmdPing, Body: reqBody})
	if err != nil {
		return nil, 0, check(failureEncode, "encode ping message: %w", err)
	}
	start := time.Now()
	if err := c.writePacket(codec.Packet{Type: codec.PacketData, Body: msgBody}, timeout); err != nil {
		return nil, 0, check(failureSend, "send ping: %w", err)
	}
	pkt, err := c.readPacket(timeout)
	if err != nil {
		return nil, 0, check(failureRead, "read tong packet: %w", err)
	}
	latency := time.Since(start)
	if pkt.Type != codec.PacketData {
		return nil, latency, check(failurePacketType, "unexpected packet type=%d", pkt.Type)
	}
	msg, err := codec.DecodeMessage(pkt.Body)
	if err != nil {
		return nil, latency, check(failureMessageDecode, "decode tong message: %w", err)
	}
	if msg.Type != codec.MessageResponse {
		return nil, latency, check(failureMessageType, "unexpected message type=%d", msg.Type)
	}
	if msg.SeqID != seq {
		return nil, latency, check(failureSeqMismatch, "unexpected seq=%d want=%d", msg.SeqID, seq)
	}
	if msg.CmdID != cmdTong {
		return nil, latency, check(failureCmdMismatch, "unexpected cmd=%d want=%d", msg.CmdID, cmdTong)
	}
	if msg.ErrCode != 0 {
		return nil, latency, check(failureErrCode, "tong error code=%s", msg.ErrCode.String())
	}
	var rsp handlerpb.SC_Tong_Rsp
	if err := proto.Unmarshal(msg.Body, &rsp); err != nil {
		return nil, latency, check(failurePayloadDecode, "unmarshal tong: %w", err)
	}
	if rsp.GetText() != expected {
		return nil, latency, check(failurePayloadMismatch, "unexpected tong text=%q want=%q", rsp.GetText(), expected)
	}
	return &rsp, latency, nil
}

func (c *client) writePacket(pkt codec.Packet, timeout time.Duration) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(timeout))
	return writePacket(c.conn, pkt)
}

func (c *client) readPacket(timeout time.Duration) (codec.Packet, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	return readPacket(c.conn)
}

func writePacket(w io.Writer, pkt codec.Packet) error {
	data, err := codec.EncodePacket(pkt)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readPacket(r io.Reader) (codec.Packet, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return codec.Packet{}, err
	}
	bodyLen := int(hdr[1])<<16 | int(hdr[2])<<8 | int(hdr[3])
	frame := make([]byte, 4+bodyLen)
	copy(frame[:4], hdr)
	if bodyLen > 0 {
		if _, err := io.ReadFull(r, frame[4:]); err != nil {
			return codec.Packet{}, err
		}
	}
	return codec.DecodePacket(frame)
}

func benchText(base string, clientID, requestID int, seq uint16) string {
	return fmt.Sprintf("%s-client-%d-req-%d-seq-%d", base, clientID, requestID+1, seq)
}

func expectedTong(text string) string {
	if text == "" {
		return "tong"
	}
	return "tong:" + text
}

func check(kind failureKind, format string, args ...any) error {
	return &checkError{kind: kind, err: fmt.Errorf(format, args...)}
}

func classifyFailure(err error) failureKind {
	var checkErr *checkError
	if asCheckError(err, &checkErr) {
		return checkErr.Kind()
	}
	return "unknown"
}

func shouldReconnect(kind failureKind) bool {
	switch kind {
	case failureRead, failureSeqMismatch, failureCmdMismatch, failurePayloadMismatch, failureMessageDecode, failureMessageType, failurePacketType:
		return true
	default:
		return false
	}
}

func asCheckError(err error, target **checkError) bool {
	if err == nil {
		return false
	}
	if v, ok := err.(*checkError); ok {
		*target = v
		return true
	}
	return false
}

func reportProgress(result *benchResult, started time.Time, every time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			elapsed := time.Since(started).Seconds()
			ok := result.succeeded.Load()
			failed := result.failed.Load()
			sent := result.sent.Load()
			qps := float64(ok) / elapsed
			fmt.Printf("progress sent=%d ok=%d failed=%d qps=%.2f elapsed=%s\n", sent, ok, failed, qps, time.Since(started).Round(time.Millisecond))
		}
	}
}

func printSummary(cfg config, result *benchResult, elapsed time.Duration) {
	summary := buildSummary(cfg, result, elapsed)
	if cfg.jsonOutput {
		data, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			fatalf("marshal summary: %v", err)
		}
		fmt.Println(string(data))
		return
	}
	if cfg.csvOutput {
		printCSVSummary(summary)
		return
	}
	fmt.Printf("benchmark done addr=%s clients=%d requests_per_client=%d sent=%d ok=%d failed=%d reconnects=%d elapsed=%s qps=%.2f\n",
		summary.Addr, summary.Clients, summary.RequestsPerClient, summary.Sent, summary.OK, summary.Failed, summary.Reconnects, elapsed.Round(time.Millisecond), summary.QPS)
	if summary.Failed > 0 {
		fmt.Printf("failures %s\n", result.failureSummary())
	}
	if summary.Latency.Count == 0 {
		return
	}
	fmt.Printf("latency min=%s avg=%s p50=%s p90=%s p99=%s max=%s\n",
		time.Duration(summary.Latency.MinNs).Round(time.Microsecond),
		time.Duration(summary.Latency.AvgNs).Round(time.Microsecond),
		time.Duration(summary.Latency.P50Ns).Round(time.Microsecond),
		time.Duration(summary.Latency.P90Ns).Round(time.Microsecond),
		time.Duration(summary.Latency.P99Ns).Round(time.Microsecond),
		time.Duration(summary.Latency.MaxNs).Round(time.Microsecond))
}

type benchSummary struct {
	Addr              string                `json:"addr"`
	Clients           int                   `json:"clients"`
	RequestsPerClient int                   `json:"requests_per_client"`
	Sent              int64                 `json:"sent"`
	OK                int64                 `json:"ok"`
	Failed            int64                 `json:"failed"`
	Reconnects        int64                 `json:"reconnects"`
	ElapsedMs         int64                 `json:"elapsed_ms"`
	QPS               float64               `json:"qps"`
	Failures          map[failureKind]int64 `json:"failures"`
	Latency           latencySummary        `json:"latency"`
}

type latencySummary struct {
	Count int   `json:"count"`
	MinNs int64 `json:"min_ns"`
	AvgNs int64 `json:"avg_ns"`
	P50Ns int64 `json:"p50_ns"`
	P90Ns int64 `json:"p90_ns"`
	P99Ns int64 `json:"p99_ns"`
	MaxNs int64 `json:"max_ns"`
}

func buildSummary(cfg config, result *benchResult, elapsed time.Duration) benchSummary {
	result.latMu.Lock()
	latencies := append([]time.Duration(nil), result.latencies...)
	result.latMu.Unlock()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	result.failMu.Lock()
	failures := make(map[failureKind]int64, len(result.failures))
	for k, v := range result.failures {
		failures[k] = v
	}
	result.failMu.Unlock()

	summary := benchSummary{
		Addr:              cfg.addr,
		Clients:           cfg.clients,
		RequestsPerClient: cfg.requests,
		Sent:              result.sent.Load(),
		OK:                result.succeeded.Load(),
		Failed:            result.failed.Load(),
		Reconnects:        result.reconnects.Load(),
		ElapsedMs:         elapsed.Milliseconds(),
		Failures:          failures,
	}
	if elapsed > 0 {
		summary.QPS = float64(summary.OK) / elapsed.Seconds()
	}
	if len(latencies) > 0 {
		summary.Latency = latencySummary{
			Count: len(latencies),
			MinNs: int64(latencies[0]),
			AvgNs: int64(avgLatency(latencies)),
			P50Ns: int64(percentile(latencies, 50)),
			P90Ns: int64(percentile(latencies, 90)),
			P99Ns: int64(percentile(latencies, 99)),
			MaxNs: int64(latencies[len(latencies)-1]),
		}
	}
	return summary
}

func printCSVSummary(s benchSummary) {
	fmt.Println("addr,clients,requests_per_client,sent,ok,failed,reconnects,elapsed_ms,qps,latency_count,min_ns,avg_ns,p50_ns,p90_ns,p99_ns,max_ns")
	fmt.Printf("%s,%d,%d,%d,%d,%d,%d,%d,%.2f,%d,%d,%d,%d,%d,%d,%d\n",
		s.Addr,
		s.Clients,
		s.RequestsPerClient,
		s.Sent,
		s.OK,
		s.Failed,
		s.Reconnects,
		s.ElapsedMs,
		s.QPS,
		s.Latency.Count,
		s.Latency.MinNs,
		s.Latency.AvgNs,
		s.Latency.P50Ns,
		s.Latency.P90Ns,
		s.Latency.P99Ns,
		s.Latency.MaxNs)
}

func (r *benchResult) failureSummary() string {
	r.failMu.Lock()
	defer r.failMu.Unlock()
	if len(r.failures) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(r.failures))
	for k := range r.failures {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, r.failures[failureKind(key)]))
	}
	return strings.Join(parts, " ")
}

func avgLatency(values []time.Duration) time.Duration {
	var total time.Duration
	for _, v := range values {
		total += v
	}
	return total / time.Duration(len(values))
}

func percentile(values []time.Duration, p int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	idx := (len(values)*p + 99) / 100
	if idx < 1 {
		idx = 1
	}
	if idx > len(values) {
		idx = len(values)
	}
	return values[idx-1]
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
