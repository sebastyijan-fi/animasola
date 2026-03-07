package p2p

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	network "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/transport"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"golang.org/x/net/proxy"
)

type torOnionTransport struct {
	upgrader transport.Upgrader
	rcmgr    network.ResourceManager
	dialer   proxy.ContextDialer
}

type onionProxyConn struct {
	net.Conn
	local  ma.Multiaddr
	remote ma.Multiaddr
}

func (c *onionProxyConn) LocalMultiaddr() ma.Multiaddr {
	return c.local
}

func (c *onionProxyConn) RemoteMultiaddr() ma.Multiaddr {
	return c.remote
}

func NewTorOnionTransport(upgrader transport.Upgrader, rcmgr network.ResourceManager, socksProxy string) (transport.Transport, error) {
	if rcmgr == nil {
		rcmgr = &network.NullResourceManager{}
	}

	dialer, err := loadTorSocksDialer(socksProxy)
	if err != nil {
		return nil, err
	}

	return &torOnionTransport{
		upgrader: upgrader,
		rcmgr:    rcmgr,
		dialer:   dialer,
	}, nil
}

func (t *torOnionTransport) Dial(ctx context.Context, raddr ma.Multiaddr, p peer.ID) (transport.CapableConn, error) {
	connScope, err := t.rcmgr.OpenConnection(network.DirOutbound, true, raddr)
	if err != nil {
		return nil, err
	}

	c, err := t.dialWithScope(ctx, raddr, p, connScope)
	if err != nil {
		connScope.Done()
		return nil, err
	}
	return c, nil
}

func (t *torOnionTransport) dialWithScope(ctx context.Context, raddr ma.Multiaddr, p peer.ID, connScope network.ConnManagementScope) (transport.CapableConn, error) {
	if err := connScope.SetPeer(p); err != nil {
		return nil, err
	}

	targetAddr, err := onionDialTarget(raddr)
	if err != nil {
		return nil, err
	}

	nconn, err := t.dialer.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		return nil, err
	}

	localMA, err := manet.FromNetAddr(nconn.LocalAddr())
	if err != nil {
		_ = nconn.Close()
		return nil, fmt.Errorf("derive local multiaddr: %w", err)
	}

	maconn := &onionProxyConn{
		Conn:   nconn,
		local:  localMA,
		remote: raddr,
	}

	direction := network.DirOutbound
	if ok, isClient, _ := network.GetSimultaneousConnect(ctx); ok && !isClient {
		direction = network.DirInbound
	}

	return t.upgrader.Upgrade(ctx, t, maconn, direction, p, connScope)
}

func (t *torOnionTransport) CanDial(addr ma.Multiaddr) bool {
	_, err := addr.ValueForProtocol(ma.P_ONION3)
	return err == nil
}

func (t *torOnionTransport) Listen(ma.Multiaddr) (transport.Listener, error) {
	return nil, fmt.Errorf("tor onion transport does not listen directly; inbound traffic must arrive via the local TCP listener and Tor hidden-service forward")
}

func (t *torOnionTransport) Protocols() []int {
	return []int{ma.P_ONION3}
}

func (t *torOnionTransport) Proxy() bool {
	return true
}

func onionDialTarget(raddr ma.Multiaddr) (string, error) {
	value, err := raddr.ValueForProtocol(ma.P_ONION3)
	if err != nil {
		return "", fmt.Errorf("missing onion3 component in remote address %s: %w", raddr, err)
	}

	host, port, ok := strings.Cut(value, ":")
	if !ok || host == "" || port == "" {
		return "", fmt.Errorf("invalid onion3 remote address %q", value)
	}

	return host + ".onion:" + port, nil
}

func loadTorSocksDialer(proxyStr string) (proxy.ContextDialer, error) {
	proxyStr = strings.TrimSpace(proxyStr)
	if proxyStr == "" {
		return nil, fmt.Errorf("tor onion transport requires a Tor SOCKS proxy endpoint")
	}

	proxyURL, err := url.Parse(proxyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid ALL_PROXY url: %w", err)
	}

	dialer, err := proxy.FromURL(proxyURL, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("failed to create Tor SOCKS dialer: %w", err)
	}

	ctxDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("Tor SOCKS dialer does not support DialContext")
	}

	return ctxDialer, nil
}
