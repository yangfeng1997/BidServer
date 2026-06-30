// Package compression 提供 zlib / LZ4 数据压缩与解压工具。
package compression

import (
	"bytes"
	"compress/zlib"
	"io"

	"github.com/pierrec/lz4/v4"
)

// CompressionType 压缩算法枚举
type CompressionType byte

const (
	CompressionZlib CompressionType = iota
	CompressionLZ4
)

// DeflateData 使用 zlib 压缩数据。
func DeflateData(data []byte) ([]byte, error) {
	var bb bytes.Buffer
	z := zlib.NewWriter(&bb)
	if _, err := z.Write(data); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return bb.Bytes(), nil
}

// InflateData 使用 zlib 解压数据。
func InflateData(data []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// IsCompressed 检测数据是否被 zlib、gzip 或 LZ4 压缩。
func IsCompressed(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	// LZ4 帧头魔数
	if IsLZ4(data) {
		return true
	}
	// zlib header
	if data[0] == 0x78 && (data[1] == 0x9C || data[1] == 0x01 || data[1] == 0xDA || data[1] == 0x5E) {
		return true
	}
	// gzip header
	if data[0] == 0x1F && data[1] == 0x8B {
		return true
	}
	return false
}

// ——— LZ4 ———

// DeflateDataLZ4 使用 LZ4 帧格式压缩数据。
func DeflateDataLZ4(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := lz4.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// InflateDataLZ4 解压 LZ4 帧格式数据。
func InflateDataLZ4(data []byte) ([]byte, error) {
	r := lz4.NewReader(bytes.NewReader(data))
	return io.ReadAll(r)
}

// IsLZ4 检测数据是否为 LZ4 帧格式。
func IsLZ4(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	// LZ4 帧魔数: 0x04 0x22 0x4D 0x18
	return data[0] == 0x04 && data[1] == 0x22 && data[2] == 0x4D && data[3] == 0x18
}

// Compress 使用指定算法压缩数据。
func Compress(data []byte, algo CompressionType) ([]byte, error) {
	switch algo {
	case CompressionLZ4:
		return DeflateDataLZ4(data)
	default:
		return DeflateData(data)
	}
}

// Decompress 自动检测压缩格式并解压。
// 根据魔数自动判断 zlib/gzip/LZ4。
func Decompress(data []byte) ([]byte, error) {
	if IsLZ4(data) {
		return InflateDataLZ4(data)
	}
	return InflateData(data)
}
