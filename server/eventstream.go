package server

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
)

// AWS EventStream binary message encoding.
// Used by kiro (AmazonCodeWhispererStreamingService.GenerateAssistantResponse).
//
// Frame layout:
//   [4] total byte length
//   [4] headers byte length
//   [4] prelude CRC32
//   [*] headers
//   [*] payload
//   [4] message CRC32

func writeEventStreamMessage(w io.Writer, eventType string, payload []byte) {
	headers := encodeHeaders(map[string]string{
		":event-type":  eventType,
		":content-type": "application/json",
		":message-type": "event",
	})

	totalLen := 12 + len(headers) + len(payload) + 4 // prelude(12) + headers + payload + message crc
	headersLen := len(headers)

	var prelude bytes.Buffer
	binary.Write(&prelude, binary.BigEndian, uint32(totalLen))
	binary.Write(&prelude, binary.BigEndian, uint32(headersLen))

	preludeCRC := crc32.ChecksumIEEE(prelude.Bytes())

	var msg bytes.Buffer
	msg.Write(prelude.Bytes())
	binary.Write(&msg, binary.BigEndian, preludeCRC)
	msg.Write(headers)
	msg.Write(payload)

	msgCRC := crc32.ChecksumIEEE(msg.Bytes())
	binary.Write(&msg, binary.BigEndian, msgCRC)

	w.Write(msg.Bytes())
}

func encodeHeaders(headers map[string]string) []byte {
	var buf bytes.Buffer
	for k, v := range headers {
		buf.WriteByte(byte(len(k)))
		buf.WriteString(k)
		buf.WriteByte(7) // type 7 = string
		binary.Write(&buf, binary.BigEndian, uint16(len(v)))
		buf.WriteString(v)
	}
	return buf.Bytes()
}
