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
	"fmt"
	"io"
)

// MaxPacketLength is the maximum length of an IPv4 packet (packet length is stored on 16 bits)
const MaxPacketLength = 1 << 16

// Protocol represents the transport protocol
type Protocol int

const (
	ProtocolTCP   Protocol = 6
	ProtocolUDP   Protocol = 17
	ProtocolOther Protocol = -1
)

func (p Protocol) String() string {
	switch p {
	case ProtocolTCP:
		return "TCP"
	case ProtocolUDP:
		return "UDP"
	default:
		return "OTHER"
	}
}

// ProtocolFromNumber returns the Protocol for the given number
func ProtocolFromNumber(number int) Protocol {
	switch number {
	case 6:
		return ProtocolTCP
	case 17:
		return ProtocolUDP
	default:
		return ProtocolOther
	}
}

const minIPv4HeaderLength = 20

// IPv4Header represents an IPv4 header
type IPv4Header struct {
	raw          []byte
	version      byte
	headerLength int
	totalLength  int
	protocol     Protocol
	source       uint32
	destination  uint32
}

// NewIPv4Header creates a new IPv4Header from raw data
func NewIPv4Header(raw []byte) *IPv4Header {
	if len(raw) < minIPv4HeaderLength {
		return nil
	}

	h := &IPv4Header{raw: raw}

	versionAndIHL := raw[0]
	h.version = versionAndIHL >> 4
	ihl := versionAndIHL & 0x0f
	h.headerLength = int(ihl) << 2

	// Limit raw to header length
	if len(raw) > h.headerLength {
		h.raw = raw[:h.headerLength]
	}

	h.totalLength = int(binary.BigEndian.Uint16(raw[2:4]))
	h.protocol = ProtocolFromNumber(int(raw[9]))
	h.source = binary.BigEndian.Uint32(raw[12:16])
	h.destination = binary.BigEndian.Uint32(raw[16:20])

	return h
}

// IsSupported returns true if this is a supported IPv4 header
func (h *IPv4Header) IsSupported() bool {
	return h.version == 4 && h.protocol != ProtocolOther
}

// GetProtocol returns the transport protocol
func (h *IPv4Header) GetProtocol() Protocol {
	return h.protocol
}

// GetHeaderLength returns the header length in bytes
func (h *IPv4Header) GetHeaderLength() int {
	return h.headerLength
}

// GetTotalLength returns the total packet length
func (h *IPv4Header) GetTotalLength() int {
	return h.totalLength
}

// SetTotalLength sets the total packet length
func (h *IPv4Header) SetTotalLength(totalLength int) {
	h.totalLength = totalLength
	binary.BigEndian.PutUint16(h.raw[2:4], uint16(totalLength))
}

// GetSource returns the source IP address
func (h *IPv4Header) GetSource() uint32 {
	return h.source
}

// GetDestination returns the destination IP address
func (h *IPv4Header) GetDestination() uint32 {
	return h.destination
}

// SetSource sets the source IP address
func (h *IPv4Header) SetSource(source uint32) {
	h.source = source
	binary.BigEndian.PutUint32(h.raw[12:16], source)
}

// SetDestination sets the destination IP address
func (h *IPv4Header) SetDestination(destination uint32) {
	h.destination = destination
	binary.BigEndian.PutUint32(h.raw[16:20], destination)
}

// SwapSourceAndDestination swaps the source and destination addresses
func (h *IPv4Header) SwapSourceAndDestination() {
	tmp := h.source
	h.SetSource(h.destination)
	h.SetDestination(tmp)
}

// GetRaw returns the raw header bytes
func (h *IPv4Header) GetRaw() []byte {
	return h.raw
}

// CopyTo copies this header to a target buffer and returns a new IPv4Header for the copy
func (h *IPv4Header) CopyTo(target []byte) *IPv4Header {
	copy(target, h.raw)
	return NewIPv4Header(target[:h.headerLength])
}

// Copy creates a copy of this header
func (h *IPv4Header) Copy() *IPv4Header {
	newRaw := make([]byte, len(h.raw))
	copy(newRaw, h.raw)
	return NewIPv4Header(newRaw)
}

// ComputeChecksum computes and sets the IPv4 header checksum
func (h *IPv4Header) ComputeChecksum() {
	// Reset checksum field
	h.SetChecksum(0)

	var sum uint32
	for i := 0; i < h.headerLength/2; i++ {
		sum += uint32(h.raw[2*i])<<8 | uint32(h.raw[2*i+1])
	}

	// Fold 32-bit sum to 16 bits
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}

	h.SetChecksum(uint16(^sum))
}

// SetChecksum sets the header checksum
func (h *IPv4Header) SetChecksum(checksum uint16) {
	binary.BigEndian.PutUint16(h.raw[10:12], checksum)
}

// GetChecksum returns the header checksum
func (h *IPv4Header) GetChecksum() uint16 {
	return binary.BigEndian.Uint16(h.raw[10:12])
}

// ReadVersion reads the IP version from a buffer
func ReadVersion(buffer []byte) int {
	if len(buffer) == 0 {
		return -1
	}
	return int(buffer[0]&0xf0) >> 4
}

// ReadLength reads the packet length from a buffer
func ReadLength(buffer []byte) int {
	if len(buffer) < 4 {
		return -1
	}
	return int(binary.BigEndian.Uint16(buffer[2:4]))
}

// IPv4Packet represents a complete IPv4 packet
type IPv4Packet struct {
	raw             []byte
	ipv4Header      *IPv4Header
	transportHeader TransportHeader
}

// NewIPv4Packet creates a new IPv4Packet from raw data
func NewIPv4Packet(raw []byte) *IPv4Packet {
	if IsVerboseEnabled() {
		LogvSimple("IPv4Packet", "IPv4Packet: "+BuildPacketString(raw))
	}

	p := &IPv4Packet{raw: raw}
	p.ipv4Header = NewIPv4Header(raw)

	if p.ipv4Header == nil || !p.ipv4Header.IsSupported() {
		LogdSimple("IPv4Packet", "Unsupported IPv4 headers")
		return p
	}

	// Limit raw to total length (AsIPv4Packet should have ensured we have enough data)
	totalLength := p.ipv4Header.GetTotalLength()
	if len(raw) > totalLength {
		p.raw = raw[:totalLength]
	}

	p.transportHeader = p.createTransportHeader()

	return p
}

// IsValid returns true if this packet has a valid transport header
func (p *IPv4Packet) IsValid() bool {
	return p.transportHeader != nil
}

func (p *IPv4Packet) createTransportHeader() TransportHeader {
	protocol := p.ipv4Header.GetProtocol()
	transportRaw := p.getRawTransport()

	// Check if we have enough data for transport header
	if len(transportRaw) < 8 { // Minimum transport header size (UDP is 8 bytes)
		return nil
	}

	switch protocol {
	case ProtocolUDP:
		return NewUDPHeader(transportRaw)
	case ProtocolTCP:
		return NewTCPHeader(transportRaw)
	default:
		return nil
	}
}

func (p *IPv4Packet) getRawTransport() []byte {
	headerLength := p.ipv4Header.GetHeaderLength()
	if len(p.raw) < int(headerLength) {
		return nil
	}
	return p.raw[headerLength:]
}

// GetIPv4Header returns the IPv4 header
func (p *IPv4Packet) GetIPv4Header() *IPv4Header {
	return p.ipv4Header
}

// GetTransportHeader returns the transport header
func (p *IPv4Packet) GetTransportHeader() TransportHeader {
	return p.transportHeader
}

// SwapSourceAndDestination swaps source and destination in both IP and transport headers
func (p *IPv4Packet) SwapSourceAndDestination() {
	p.ipv4Header.SwapSourceAndDestination()
	p.transportHeader.SwapSourceAndDestination()
}

// GetRaw returns the raw packet bytes
func (p *IPv4Packet) GetRaw() []byte {
	return p.raw
}

// GetRawLength returns the length of the raw packet
func (p *IPv4Packet) GetRawLength() int {
	return len(p.raw)
}

// GetPayload returns the payload portion of the packet
func (p *IPv4Packet) GetPayload() []byte {
	headersLength := p.ipv4Header.GetHeaderLength() + p.transportHeader.GetHeaderLength()
	return p.raw[headersLength:]
}

// GetPayloadLength returns the length of the payload
func (p *IPv4Packet) GetPayloadLength() int {
	return len(p.raw) - p.ipv4Header.GetHeaderLength() - p.transportHeader.GetHeaderLength()
}

// ComputeChecksums computes both IP and transport checksums
func (p *IPv4Packet) ComputeChecksums() {
	p.ipv4Header.ComputeChecksum()
	p.transportHeader.ComputeChecksum(p.ipv4Header, p.GetPayload())
}

// IPv4PacketBuffer is a buffer for reading IPv4 packets from a stream
type IPv4PacketBuffer struct {
	buffer []byte
	pos    int
}

// NewIPv4PacketBuffer creates a new IPv4PacketBuffer
func NewIPv4PacketBuffer() *IPv4PacketBuffer {
	return &IPv4PacketBuffer{
		buffer: make([]byte, MaxPacketLength),
		pos:    0,
	}
}

// ReadFrom reads data from a reader into the buffer
func (pb *IPv4PacketBuffer) ReadFrom(r io.Reader) (int, error) {
	n, err := r.Read(pb.buffer[pb.pos:])
	if n > 0 {
		pb.pos += n
	}
	return n, err
}

func (pb *IPv4PacketBuffer) getAvailablePacketLength() int {
	if pb.pos < 4 {
		return 0
	}

	length := ReadLength(pb.buffer[:pb.pos])
	if length == -1 {
		return 0
	}

	version := ReadVersion(pb.buffer[:pb.pos])
	if version != 4 {
		return 0
	}

	if length > pb.pos {
		return 0
	}

	return length
}

// AsIPv4Packet returns the next complete IPv4 packet or nil if not available
func (pb *IPv4PacketBuffer) AsIPv4Packet() *IPv4Packet {
	length := pb.getAvailablePacketLength()
	if length == 0 {
		return nil
	}

	packetData := make([]byte, length)
	copy(packetData, pb.buffer[:length])

	return NewIPv4Packet(packetData)
}

// Next advances past the current packet
func (pb *IPv4PacketBuffer) Next() {
	length := pb.getAvailablePacketLength()
	if length > 0 {
		copy(pb.buffer, pb.buffer[length:pb.pos])
		pb.pos -= length
	}
}

// ConnectionId identifies a connection
type ConnectionId struct {
	protocol   Protocol
	sourceIp   uint32
	sourcePort uint16
	destIp     uint32
	destPort   uint16
	idString   string
}

// NewConnectionId creates a new ConnectionId
func NewConnectionId(protocol Protocol, sourceIp uint32, sourcePort uint16, destIp uint32, destPort uint16) *ConnectionId {
	id := &ConnectionId{
		protocol:   protocol,
		sourceIp:   sourceIp,
		sourcePort: sourcePort,
		destIp:     destIp,
		destPort:   destPort,
	}
	id.idString = fmt.Sprintf("%s %s -> %s", protocol, IPPortToString(sourceIp, sourcePort), IPPortToString(destIp, destPort))
	return id
}

// ConnectionIdFrom creates a ConnectionId from headers
func ConnectionIdFrom(ipv4Header *IPv4Header, transportHeader TransportHeader) *ConnectionId {
	return NewConnectionId(
		ipv4Header.GetProtocol(),
		ipv4Header.GetSource(),
		uint16(transportHeader.GetSourcePort()),
		ipv4Header.GetDestination(),
		uint16(transportHeader.GetDestinationPort()),
	)
}

// GetProtocol returns the protocol
func (id *ConnectionId) GetProtocol() Protocol {
	return id.protocol
}

// GetSourceIp returns the source IP
func (id *ConnectionId) GetSourceIp() uint32 {
	return id.sourceIp
}

// GetSourcePort returns the source port
func (id *ConnectionId) GetSourcePort() int {
	return int(id.sourcePort)
}

// GetDestinationIp returns the destination IP
func (id *ConnectionId) GetDestinationIp() uint32 {
	return id.destIp
}

// GetDestinationPort returns the destination port
func (id *ConnectionId) GetDestinationPort() int {
	return int(id.destPort)
}

// Equals checks equality with another ConnectionId
func (id *ConnectionId) Equals(other *ConnectionId) bool {
	return id.sourceIp == other.sourceIp &&
		id.sourcePort == other.sourcePort &&
		id.destIp == other.destIp &&
		id.destPort == other.destPort &&
		id.protocol == other.protocol
}

func (id *ConnectionId) String() string {
	return id.idString
}
