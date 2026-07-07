package codec

import (
	"testing"

	"projectbid/server/conn/packet"
)

func TestPomeloPacketEncoder_Encode(t *testing.T) {
	e := NewPomeloPacketEncoder()

	tests := []struct {
		name string
		typ  packet.Type
		data []byte
	}{
		{"握手包", packet.Handshake, []byte(`{"sys":{}}`)},
		{"数据包", packet.Data, []byte("hello world")},
		{"空数据", packet.Heartbeat, []byte{}},
		{"大端数据", packet.Kick, make([]byte, 1000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := e.Encode(tt.typ, tt.data)
			if err != nil {
				t.Fatalf("编码失败: %v", err)
			}
			if len(encoded) != len(tt.data)+HeadLength {
				t.Fatalf("编码长度错误: 期望 %d, 实际 %d", len(tt.data)+HeadLength, len(encoded))
			}
			if packet.Type(encoded[0]) != tt.typ {
				t.Fatalf("类型字段错误: 期望 %d, 实际 %d", tt.typ, encoded[0])
			}
			size := BytesToInt(encoded[1:HeadLength])
			if size != len(tt.data) {
				t.Fatalf("长度字段错误: 期望 %d, 实际 %d", len(tt.data), size)
			}
		})
	}
}

func TestPomeloPacketEncoder_Encode_InvalidType(t *testing.T) {
	e := NewPomeloPacketEncoder()
	_, err := e.Encode(packet.Type(0x06), []byte{})
	if err == nil {
		t.Fatal("应返回错误：无效的数据包类型")
	}
}

func TestPomeloPacketEncoder_Encode_TooLarge(t *testing.T) {
	e := NewPomeloPacketEncoder()
	_, err := e.Encode(packet.Data, make([]byte, MaxPacketSize+1))
	if err == nil {
		t.Fatal("应返回错误：数据超大小限制")
	}
}

func TestPomeloPacketDecoder_Decode(t *testing.T) {
	e := NewPomeloPacketEncoder()
	d := NewPomeloPacketDecoder()

	// 编码多个数据包
	p1, _ := e.Encode(packet.Handshake, []byte("hello"))
	p2, _ := e.Encode(packet.Data, []byte("world"))
	combined := append(p1, p2...)

	packets, err := d.Decode(combined)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if len(packets) != 2 {
		t.Fatalf("数据包数量错误: 期望 2, 实际 %d", len(packets))
	}
	if packets[0].Type != packet.Handshake || string(packets[0].Data) != "hello" {
		t.Fatal("第一个数据包内容错误")
	}
	if packets[1].Type != packet.Data || string(packets[1].Data) != "world" {
		t.Fatal("第二个数据包内容错误")
	}
}

func TestPomeloPacketDecoder_Decode_Partial(t *testing.T) {
	d := NewPomeloPacketDecoder()

	// 数据不足时返回 nil, nil
	packets, err := d.Decode([]byte{0x01, 0x00, 0x00}) // 只有 3 字节，不够头
	if err != nil {
		t.Fatalf("部分数据不应报错: %v", err)
	}
	if packets != nil {
		t.Fatal("部分数据应返回 nil packets")
	}
}

func TestPomeloPacketDecoder_RoundTrip(t *testing.T) {
	e := NewPomeloPacketEncoder()
	d := NewPomeloPacketDecoder()

	testData := []byte("round trip test data with 中文")
	encoded, _ := e.Encode(packet.HandshakeAck, testData)
	packets, err := d.Decode(encoded)
	if err != nil {
		t.Fatalf("往返测试失败: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("应解码出 1 个数据包，实际 %d", len(packets))
	}
	if packets[0].Type != packet.HandshakeAck {
		t.Fatal("类型不匹配")
	}
	if string(packets[0].Data) != string(testData) {
		t.Fatal("数据不匹配")
	}
}

func TestParseHeader(t *testing.T) {
	header := []byte{byte(packet.Data), 0x00, 0x00, 0x05}
	size, typ, err := ParseHeader(header)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if size != 5 {
		t.Fatalf("长度错误: 期望 5, 实际 %d", size)
	}
	if typ != packet.Data {
		t.Fatal("类型错误")
	}
}

func TestParseHeader_InvalidType(t *testing.T) {
	header := []byte{0xFF, 0x00, 0x00, 0x01}
	_, _, err := ParseHeader(header)
	if err == nil {
		t.Fatal("应返回错误：无效类型")
	}
}

func TestBytesToInt(t *testing.T) {
	if BytesToInt([]byte{0x00, 0x00, 0x05}) != 5 {
		t.Fatal("BytesToInt(5) 失败")
	}
	if BytesToInt([]byte{0x00, 0x01, 0x00}) != 256 {
		t.Fatal("BytesToInt(256) 失败")
	}
	if BytesToInt([]byte{0x01, 0x00, 0x00}) != 65536 {
		t.Fatal("BytesToInt(65536) 失败")
	}
}

func TestIntToBytes(t *testing.T) {
	b := IntToBytes(5)
	if len(b) != 3 || b[0] != 0 || b[1] != 0 || b[2] != 5 {
		t.Fatal("IntToBytes(5) 失败")
	}
	b = IntToBytes(256)
	if len(b) != 3 || b[0] != 0 || b[1] != 1 || b[2] != 0 {
		t.Fatal("IntToBytes(256) 失败")
	}
}

func TestIntToBytes_RoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 255, 256, 65535, 65536, 1 << 24 - 1} {
		if BytesToInt(IntToBytes(n)) != n {
			t.Fatalf("往返失败: %d", n)
		}
	}
}
