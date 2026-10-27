package com.genymobile.gnirehtet;

import android.app.Activity;
import android.content.Intent;
import android.net.VpnService;
import android.os.Bundle;
import android.util.Log;

import java.net.InetAddress;

/**
 * This (invisible) activity receives the {@link #ACTION_GNIREHTET_START START} and
 * {@link #ACTION_GNIREHTET_STOP} actions from the command line.
 * <p>
 * Recent versions of Android refuse to directly start a {@link android.app.Service Service} or a
 * {@link android.content.BroadcastReceiver BroadcastReceiver}, so actions are always managed by
 * this activity.
 */
public class GnirehtetActivity extends Activity {

    private static final String TAG = GnirehtetActivity.class.getSimpleName();

    public static final String ACTION_GNIREHTET_START = "com.genymobile.gnirehtet.START";
    public static final String ACTION_GNIREHTET_STOP = "com.genymobile.gnirehtet.STOP";

    public static final String EXTRA_DNS_SERVERS = "dnsServers";
    public static final String EXTRA_ROUTES = "routes";
    /** 可选：直接传完整 VpnConfiguration（Parcelable） */
    public static final String EXTRA_VPN_CONFIGURATION = "vpnConfiguration";
    /** 可选全量参数：vpnAddress, vpnPrefixLength, mtu, searchDomains, allowedApplications, disallowedApplications, allowBypass, allowIpv6, excludeRoutes */
    public static final String EXTRA_VPN_ADDRESS = "vpnAddress";
    public static final String EXTRA_VPN_PREFIX_LENGTH = "vpnPrefixLength";
    public static final String EXTRA_MTU = "mtu";
    public static final String EXTRA_SEARCH_DOMAINS = "searchDomains";
    public static final String EXTRA_ALLOWED_APPLICATIONS = "allowedApplications";
    public static final String EXTRA_DISALLOWED_APPLICATIONS = "disallowedApplications";
    public static final String EXTRA_ALLOW_BYPASS = "allowBypass";
    public static final String EXTRA_ALLOW_IPV6 = "allowIpv6";
    public static final String EXTRA_EXCLUDE_ROUTES = "excludeRoutes";

    private static final int VPN_REQUEST_CODE = 0;

    private VpnConfiguration requestedConfig;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        handleIntent(getIntent());
    }

    private void handleIntent(Intent intent) {
        String action = intent.getAction();
        Log.d(TAG, "Received request " + action);
        boolean finish = true;
        if (ACTION_GNIREHTET_START.equals(action)) {
            VpnConfiguration config = createConfig(intent);
            finish = startGnirehtet(config);
        } else if (ACTION_GNIREHTET_STOP.equals(action)) {
            stopGnirehtet();
        }

        if (finish) {
            finish();
        }
    }

    private static VpnConfiguration createConfig(Intent intent) {
        VpnConfiguration parcel = intent.getParcelableExtra(EXTRA_VPN_CONFIGURATION);
        if (parcel != null) {
            return parcel;
        }
        String[] dnsServers = intent.getStringArrayExtra(EXTRA_DNS_SERVERS);
        if (dnsServers == null) dnsServers = new String[0];
        String[] routes = intent.getStringArrayExtra(EXTRA_ROUTES);
        if (routes == null) routes = new String[0];

        InetAddress vpnAddr = null;
        String vpnAddrStr = intent.getStringExtra(EXTRA_VPN_ADDRESS);
        if (vpnAddrStr != null && !vpnAddrStr.isEmpty()) {
            vpnAddr = Net.toInetAddress(vpnAddrStr);
        }
        int vpnPrefix = intent.getIntExtra(EXTRA_VPN_PREFIX_LENGTH, 32);
        int mtu = intent.getIntExtra(EXTRA_MTU, 0);
        String[] searchDomains = intent.getStringArrayExtra(EXTRA_SEARCH_DOMAINS);
        if (searchDomains == null) searchDomains = new String[0];
        String[] allowed = intent.getStringArrayExtra(EXTRA_ALLOWED_APPLICATIONS);
        if (allowed == null) allowed = new String[0];
        String[] disallowed = intent.getStringArrayExtra(EXTRA_DISALLOWED_APPLICATIONS);
        if (disallowed == null) disallowed = new String[0];
        boolean allowBypass = intent.getBooleanExtra(EXTRA_ALLOW_BYPASS, false);
        boolean allowIpv6 = intent.getBooleanExtra(EXTRA_ALLOW_IPV6, false);
        String[] excludeStr = intent.getStringArrayExtra(EXTRA_EXCLUDE_ROUTES);
        CIDR[] excludeRoutes = new CIDR[0];
        if (excludeStr != null && excludeStr.length > 0) {
            try {
                excludeRoutes = Net.toCIDRs(excludeStr);
            } catch (Exception ignored) { }
        }

        return new VpnConfiguration(Net.toInetAddresses(dnsServers), Net.toCIDRs(routes),
                vpnAddr, vpnPrefix, mtu, searchDomains, allowed, disallowed,
                allowBypass, true, allowIpv6, excludeRoutes);
    }

    private boolean startGnirehtet(VpnConfiguration config) {
        Intent vpnIntent = VpnService.prepare(this);
        if (vpnIntent == null) {
            Log.d(TAG, "VPN was already authorized");
            GnirehtetService.start(this, config);
            return true;
        }
        Log.w(TAG, "VPN requires the authorization from the user, requesting...");
        requestAuthorization(vpnIntent, config);
        return false;
    }

    private void stopGnirehtet() {
        GnirehtetService.stop(this);
    }

    private void requestAuthorization(Intent vpnIntent, VpnConfiguration config) {
        this.requestedConfig = config;
        startActivityForResult(vpnIntent, VPN_REQUEST_CODE);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == VPN_REQUEST_CODE && resultCode == RESULT_OK) {
            GnirehtetService.start(this, requestedConfig);
        }
        requestedConfig = null;
        finish();
    }
}
