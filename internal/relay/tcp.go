/*
 * Copyright (C) 2017 Genymobile
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package relay

import (
	"encoding/binary"
)

// TCP flags
const (
	TCPFlagFIN = 1 << 0
	TCPFlagSYN = 1 << 1
	TCPFlagRST = 1 << 2
	TCPFlagPSH = 1 << 3
	TCPFlagACK = 1 << 4
	TCPFlagURG = 1 << 5
)

// TCPHeader represents a TCP header
type TCPHeader struct {
	raw                   []byte
	sourcePort            int
	destinationPort       int
	headerLength          int
	sequenceNumber        uint32
	acknowledgementNumber uint32
	flags                 int
	window                int
}

// NewTCPHeader creates a new TCPHeader from raw data
func NewTCPHeader(raw []byte) *TCPHeader {
	// TCP headers are at least 20 bytes (without options)
	if len(raw) < 20 {
		return nil
	}

	dataOffsetAndFlags := binary.BigEndian.Uint16(raw[12:14])
	headerLength := int((dataOffsetAndFlags & 0xf000) >> 10)
	if headerLength < 20 {
		return nil
	}
	if len(raw) < headerLength {
		return nil
	}

	h := &TCPHeader{raw: raw[:headerLength]}
	h.headerLength = headerLength

	h.sourcePort = int(binary.BigEndian.Uint16(raw[0:2]))
	h.destinationPort = int(binary.BigEndian.Uint16(raw[2:4]))

	h.sequenceNumber = binary.BigEndian.Uint32(raw[4:8])
	h.acknowledgementNumber = binary.BigEndian.Uint32(raw[8:12])

	h.flags = int(dataOffsetAndFlags & 0x1ff)
	h.window = int(binary.BigEndian.Uint16(raw[14:16]))

	return h
}

// GetWindow returns the window size
func (h *TCPHeader) GetWindow() int {
	return h.window
}

// GetSourcePort returns the source port
func (h *TCPHeader) GetSourcePort() int {
	return h.sourcePort
}

// GetDestinationPort returns the destination port
func (h *TCPHeader) GetDestinationPort() int {
	return h.destinationPort
}

// SetSourcePort sets the source port
func (h *TCPHeader) SetSourcePort(port int) {
	h.sourcePort = port
	binary.BigEndian.PutUint16(h.raw[0:2], uint16(port))
}

// SetDestinationPort sets the destination port
func (h *TCPHeader) SetDestinationPort(port int) {
	h.destinationPort = port
	binary.BigEndian.PutUint16(h.raw[2:4], uint16(port))
}

// GetSequenceNumber returns the sequence number
func (h *TCPHeader) GetSequenceNumber() uint32 {
	return h.sequenceNumber
}

// SetSequenceNumber sets the sequence number
func (h *TCPHeader) SetSequenceNumber(seq uint32) {
	h.sequenceNumber = seq
	binary.BigEndian.PutUint32(h.raw[4:8], seq)
}

// GetAcknowledgementNumber returns the acknowledgement number
func (h *TCPHeader) GetAcknowledgementNumber() uint32 {
	return h.acknowledgementNumber
}

// SetAcknowledgementNumber sets the acknowledgement number
func (h *TCPHeader) SetAcknowledgementNumber(ack uint32) {
	h.acknowledgementNumber = ack
	binary.BigEndian.PutUint32(h.raw[8:12], ack)
}

// GetHeaderLength returns the header length
func (h *TCPHeader) GetHeaderLength() int {
	return h.headerLength
}

// SetPayloadLength does nothing for TCP
func (h *TCPHeader) SetPayloadLength(payloadLength int) {
	// do nothing
}

// GetFlags returns the TCP flags
func (h *TCPHeader) GetFlags() int {
	return h.flags
}

// SetFlags sets the TCP flags
func (h *TCPHeader) SetFlags(flags int) {
	h.flags = flags
	dataOffsetAndFlags := binary.BigEndian.Uint16(h.raw[12:14])
	dataOffsetAndFlags = (dataOffsetAndFlags & 0xfe00) | uint16(flags&0x1ff)
	binary.BigEndian.PutUint16(h.raw[12:14], dataOffsetAndFlags)
}

// ShrinkOptions removes TCP options, leaving only a 20-byte header
func (h *TCPHeader) ShrinkOptions() {
	h.setDataOffset(5)
	h.raw = h.raw[:20]
}

func (h *TCPHeader) setDataOffset(dataOffset int) {
	dataOffsetAndFlags := binary.BigEndian.Uint16(h.raw[12:14])
	dataOffsetAndFlags = (dataOffsetAndFlags & 0x0fff) | uint16(dataOffset<<12)
	binary.BigEndian.PutUint16(h.raw[12:14], dataOffsetAndFlags)
	h.headerLength = dataOffset << 2
}

// IsFin returns true if FIN flag is set
func (h *TCPHeader) IsFin() bool {
	return (h.flags & TCPFlagFIN) != 0
}

// IsSyn returns true if SYN flag is set
func (h *TCPHeader) IsSyn() bool {
	return (h.flags & TCPFlagSYN) != 0
}

// IsRst returns true if RST flag is set
func (h *TCPHeader) IsRst() bool {
	return (h.flags & TCPFlagRST) != 0
}

// IsPsh returns true if PSH flag is set
func (h *TCPHeader) IsPsh() bool {
	return (h.flags & TCPFlagPSH) != 0
}

// IsAck returns true if ACK flag is set
func (h *TCPHeader) IsAck() bool {
	return (h.flags & TCPFlagACK) != 0
}

// IsUrg returns true if URG flag is set
func (h *TCPHeader) IsUrg() bool {
	return (h.flags & TCPFlagURG) != 0
}

// GetRaw returns the raw header bytes
func (h *TCPHeader) GetRaw() []byte {
	return h.raw
}

// CopyTo copies the header to target buffer and returns a new TCPHeader
func (h *TCPHeader) CopyTo(target []byte) TransportHeader {
	copy(target, h.raw)
	return NewTCPHeader(target[:h.headerLength])
}

// Copy creates a copy of this header
func (h *TCPHeader) Copy() *TCPHeader {
	newRaw := make([]byte, len(h.raw))
	copy(newRaw, h.raw)
	return NewTCPHeader(newRaw)
}

// ComputeChecksum computes and sets the TCP checksum
func (h *TCPHeader) ComputeChecksum(ipv4Header *IPv4Header, payload []byte) {
	// Pseudo-header checksum (cf rfc793 section 3.1)
	source := ipv4Header.GetSource()
	destination := ipv4Header.GetDestination()
	length := ipv4Header.GetTotalLength() - ipv4Header.GetHeaderLength()

	var sum uint32
	sum += source >> 16
	sum += source & 0xffff
	sum += destination >> 16
	sum += destination & 0xffff
	sum += uint32(ProtocolTCP)
	sum += uint32(length)

	// Reset checksum field
	h.SetChecksum(0)

	// Add header bytes
	for i := 0; i < h.headerLength/2; i++ {
		sum += uint32(h.raw[2*i])<<8 | uint32(h.raw[2*i+1])
	}

	// Add payload bytes
	payloadLength := length - h.headerLength
	for i := 0; i < payloadLength/2; i++ {
		sum += uint32(payload[2*i])<<8 | uint32(payload[2*i+1])
	}
	if payloadLength%2 != 0 {
		sum += uint32(payload[payloadLength-1]) << 8
	}

	// Fold 32-bit sum to 16 bits
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}

	h.SetChecksum(uint16(^sum))
}

// SetChecksum sets the checksum
func (h *TCPHeader) SetChecksum(checksum uint16) {
	binary.BigEndian.PutUint16(h.raw[16:18], checksum)
}

// GetChecksum returns the checksum
func (h *TCPHeader) GetChecksum() uint16 {
	return binary.BigEndian.Uint16(h.raw[16:18])
}

// SwapSourceAndDestination swaps source and destination ports
func (h *TCPHeader) SwapSourceAndDestination() {
	tmp := h.sourcePort
	h.SetSourcePort(h.destinationPort)
	h.SetDestinationPort(tmp)
}
