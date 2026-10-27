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

package com.genymobile.gnirehtet;

import android.os.Parcel;
import android.os.Parcelable;

import java.net.InetAddress;
import java.net.UnknownHostException;

public class VpnConfiguration implements Parcelable {

    private final InetAddress[] dnsServers;
    private final CIDR[] routes;
    /** VPN 虚拟网卡地址，null 用默认 10.0.0.2/32 */
    private final InetAddress vpnAddress;
    private final int vpnPrefixLength;
    /** MTU，0 表示用默认 0x4000 */
    private final int mtu;
    private final String[] searchDomains;
    /** 白名单包名，非空时仅这些应用走 VPN */
    private final String[] allowedApplications;
    /** 黑名单包名，非空时这些应用不走 VPN（与白名单二选一） */
    private final String[] disallowedApplications;
    private final boolean allowBypass;
    private final boolean allowIpv4;
    private final boolean allowIpv6;
    /** 不走 VPN 的路由，API 29+ */
    private final CIDR[] excludeRoutes;

    public VpnConfiguration() {
        this.dnsServers = new InetAddress[0];
        this.routes = new CIDR[0];
        this.vpnAddress = null;
        this.vpnPrefixLength = 32;
        this.mtu = 0;
        this.searchDomains = new String[0];
        this.allowedApplications = new String[0];
        this.disallowedApplications = new String[0];
        this.allowBypass = false;
        this.allowIpv4 = true;
        this.allowIpv6 = false;
        this.excludeRoutes = new CIDR[0];
    }

    public VpnConfiguration(InetAddress[] dnsServers, CIDR[] routes) {
        this.dnsServers = dnsServers != null ? dnsServers : new InetAddress[0];
        this.routes = routes != null ? routes : new CIDR[0];
        this.vpnAddress = null;
        this.vpnPrefixLength = 32;
        this.mtu = 0;
        this.searchDomains = new String[0];
        this.allowedApplications = new String[0];
        this.disallowedApplications = new String[0];
        this.allowBypass = false;
        this.allowIpv4 = true;
        this.allowIpv6 = false;
        this.excludeRoutes = new CIDR[0];
    }

    /** 全量参数构造（用于 Intent 解析或动态修改） */
    public VpnConfiguration(InetAddress[] dnsServers, CIDR[] routes,
            InetAddress vpnAddress, int vpnPrefixLength, int mtu,
            String[] searchDomains, String[] allowedApplications, String[] disallowedApplications,
            boolean allowBypass, boolean allowIpv4, boolean allowIpv6, CIDR[] excludeRoutes) {
        this.dnsServers = dnsServers != null ? dnsServers : new InetAddress[0];
        this.routes = routes != null ? routes : new CIDR[0];
        this.vpnAddress = vpnAddress;
        this.vpnPrefixLength = vpnPrefixLength <= 0 ? 32 : vpnPrefixLength;
        this.mtu = mtu < 0 ? 0 : mtu;
        this.searchDomains = searchDomains != null ? searchDomains : new String[0];
        this.allowedApplications = allowedApplications != null ? allowedApplications : new String[0];
        this.disallowedApplications = disallowedApplications != null ? disallowedApplications : new String[0];
        this.allowBypass = allowBypass;
        this.allowIpv4 = allowIpv4;
        this.allowIpv6 = allowIpv6;
        this.excludeRoutes = excludeRoutes != null ? excludeRoutes : new CIDR[0];
    }

    private VpnConfiguration(Parcel source) {
        int dnsCount = source.readInt();
        dnsServers = new InetAddress[dnsCount];
        try {
            for (int i = 0; i < dnsCount; ++i) {
                dnsServers[i] = InetAddress.getByAddress(source.createByteArray());
            }
        } catch (UnknownHostException e) {
            throw new AssertionError("Invalid address", e);
        }
        routes = source.createTypedArray(CIDR.CREATOR);
        byte[] vpnAddrBytes = source.createByteArray();
        vpnAddress = vpnAddrBytes != null ? Net.toInetAddress(vpnAddrBytes) : null;
        vpnPrefixLength = source.readInt();
        mtu = source.readInt();
        searchDomains = source.createStringArray();
        allowedApplications = source.createStringArray();
        disallowedApplications = source.createStringArray();
        allowBypass = source.readInt() != 0;
        allowIpv4 = source.readInt() != 0;
        allowIpv6 = source.readInt() != 0;
        excludeRoutes = source.createTypedArray(CIDR.CREATOR);
    }

    public InetAddress[] getDnsServers() { return dnsServers; }
    public CIDR[] getRoutes() { return routes; }
    public InetAddress getVpnAddress() { return vpnAddress; }
    public int getVpnPrefixLength() { return vpnPrefixLength; }
    public int getMtu() { return mtu; }
    public String[] getSearchDomains() { return searchDomains; }
    public String[] getAllowedApplications() { return allowedApplications; }
    public String[] getDisallowedApplications() { return disallowedApplications; }
    public boolean isAllowBypass() { return allowBypass; }
    public boolean isAllowIpv4() { return allowIpv4; }
    public boolean isAllowIpv6() { return allowIpv6; }
    public CIDR[] getExcludeRoutes() { return excludeRoutes; }

    @Override
    public void writeToParcel(Parcel dest, int flags) {
        dest.writeInt(dnsServers.length);
        for (InetAddress addr : dnsServers) {
            dest.writeByteArray(addr.getAddress());
        }
        dest.writeTypedArray(routes, 0);
        dest.writeByteArray(vpnAddress != null ? vpnAddress.getAddress() : null);
        dest.writeInt(vpnPrefixLength);
        dest.writeInt(mtu);
        dest.writeStringArray(searchDomains);
        dest.writeStringArray(allowedApplications);
        dest.writeStringArray(disallowedApplications);
        dest.writeInt(allowBypass ? 1 : 0);
        dest.writeInt(allowIpv4 ? 1 : 0);
        dest.writeInt(allowIpv6 ? 1 : 0);
        dest.writeTypedArray(excludeRoutes, 0);
    }

    @Override
    public int describeContents() {
        return 0;
    }

    public static final Creator<VpnConfiguration> CREATOR = new Creator<VpnConfiguration>() {
        @Override
        public VpnConfiguration createFromParcel(Parcel source) {
            return new VpnConfiguration(source);
        }

        @Override
        public VpnConfiguration[] newArray(int size) {
            return new VpnConfiguration[size];
        }
    };
}
