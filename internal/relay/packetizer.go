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
	"io"
)

// Packetizer converts from level 5 to level 3 by appending correct IP and transport headers
type Packetizer struct {
	buffer                  []byte
	payloadOffset           int
	responseIPv4Header      *IPv4Header
	responseTransportHeader TransportHeader
}

// NewPacketizer creates a new Packetizer
func NewPacketizer(ipv4Header *IPv4Header, transportHeader TransportHeader) *Packetizer {
	p := &Packetizer{
		buffer: make([]byte, MaxPacketLength),
	}

	// Copy headers to buffer
	p.responseIPv4Header = ipv4Header.CopyTo(p.buffer)
	offset := ipv4Header.GetHeaderLength()
	p.responseTransportHeader = transportHeader.CopyTo(p.buffer[offset:])
	p.payloadOffset = offset + transportHeader.GetHeaderLength()

	return p
}

// GetResponseIPv4Header returns the response IPv4 header
func (p *Packetizer) GetResponseIPv4Header() *IPv4Header {
	return p.responseIPv4Header
}

// GetResponseTransportHeader returns the response transport header
func (p *Packetizer) GetResponseTransportHeader() TransportHeader {
	return p.responseTransportHeader
}

// PacketizeEmptyPayload creates a packet with empty payload
func (p *Packetizer) PacketizeEmptyPayload() *IPv4Packet {
	return p.inflate(0)
}

// Packetize reads from a reader and creates an IPv4 packet
func (p *Packetizer) Packetize(r io.Reader, maxChunkSize int) (*IPv4Packet, error) {
	if maxChunkSize <= 0 {
		maxChunkSize = len(p.buffer) - p.payloadOffset
	}

	payloadBuf := p.buffer[p.payloadOffset : p.payloadOffset+maxChunkSize]
	n, err := r.Read(payloadBuf)
	if err != nil {
		if err == io.EOF && n == 0 {
			return nil, nil
		}
		return nil, err
	}

	if n == 0 {
		return nil, nil
	}

	return p.inflate(n), nil
}

// PacketizeAll reads from a reader and creates an IPv4 packet using maximum capacity
func (p *Packetizer) PacketizeAll(r io.Reader) (*IPv4Packet, error) {
	return p.Packetize(r, 0)
}

func (p *Packetizer) inflate(payloadLength int) *IPv4Packet {
	totalLength := p.payloadOffset + payloadLength

	ipv4HeaderLength := p.responseIPv4Header.GetHeaderLength()
	transportHeaderLength := p.responseTransportHeader.GetHeaderLength()
	fullTotalLength := ipv4HeaderLength + transportHeaderLength + payloadLength

	p.responseIPv4Header.SetTotalLength(fullTotalLength)
	p.responseTransportHeader.SetPayloadLength(payloadLength)

	// Create packet from buffer
	packetData := make([]byte, totalLength)
	copy(packetData, p.buffer[:totalLength])

	packet := NewIPv4Packet(packetData)
	packet.ComputeChecksums()
	return packet
}
