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

const udpHeaderLength = 8

// UDPHeader represents a UDP header
type UDPHeader struct {
	raw             []byte
	sourcePort      int
	destinationPort int
}

// NewUDPHeader creates a new UDPHeader from raw data
func NewUDPHeader(raw []byte) *UDPHeader {
	if len(raw) < udpHeaderLength {
		return nil
	}
	h := &UDPHeader{raw: raw[:udpHeaderLength]}
	h.sourcePort = int(binary.BigEndian.Uint16(raw[0:2]))
	h.destinationPort = int(binary.BigEndian.Uint16(raw[2:4]))
	return h
}

// GetSourcePort returns the source port
func (h *UDPHeader) GetSourcePort() int {
	return h.sourcePort
}

// GetDestinationPort returns the destination port
func (h *UDPHeader) GetDestinationPort() int {
	return h.destinationPort
}

// SetSourcePort sets the source port
func (h *UDPHeader) SetSourcePort(port int) {
	h.sourcePort = port
	binary.BigEndian.PutUint16(h.raw[0:2], uint16(port))
}

// SetDestinationPort sets the destination port
func (h *UDPHeader) SetDestinationPort(port int) {
	h.destinationPort = port
	binary.BigEndian.PutUint16(h.raw[2:4], uint16(port))
}

// GetHeaderLength returns the header length (always 8 for UDP)
func (h *UDPHeader) GetHeaderLength() int {
	return udpHeaderLength
}

// SetPayloadLength sets the payload length in the UDP header
func (h *UDPHeader) SetPayloadLength(payloadLength int) {
	length := h.GetHeaderLength() + payloadLength
	binary.BigEndian.PutUint16(h.raw[4:6], uint16(length))
}

// GetRaw returns the raw header bytes
func (h *UDPHeader) GetRaw() []byte {
	return h.raw
}

// CopyTo copies the header to target buffer and returns a new UDPHeader
func (h *UDPHeader) CopyTo(target []byte) TransportHeader {
	copy(target, h.raw)
	return NewUDPHeader(target[:udpHeaderLength])
}

// ComputeChecksum disables checksum validation for UDP
func (h *UDPHeader) ComputeChecksum(ipv4Header *IPv4Header, payload []byte) {
	// disable checksum validation
	binary.BigEndian.PutUint16(h.raw[6:8], 0)
}

// SwapSourceAndDestination swaps source and destination ports
func (h *UDPHeader) SwapSourceAndDestination() {
	tmp := h.sourcePort
	h.SetSourcePort(h.destinationPort)
	h.SetDestinationPort(tmp)
}
