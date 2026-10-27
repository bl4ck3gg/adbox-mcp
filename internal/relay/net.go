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
	"fmt"
	"net"
)

// ToInetAddress converts an IPv4 integer to net.IP
func ToInetAddress(ipAddr uint32) net.IP {
	return net.IPv4(
		byte(ipAddr>>24),
		byte((ipAddr>>16)&0xff),
		byte((ipAddr>>8)&0xff),
		byte(ipAddr&0xff),
	)
}

// IPToUint32 converts net.IP to uint32
func IPToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// IPPortToString converts IP and port to string
func IPPortToString(ip uint32, port uint16) string {
	return fmt.Sprintf("%s:%d", ToInetAddress(ip).String(), port)
}

// NetAddrString returns string representation of address
func NetAddrString(addr net.Addr) string {
	return addr.String()
}
