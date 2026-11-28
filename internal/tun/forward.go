package tun

import (
	"net"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/xjasonlyu/tun2socks/v2/tunnel"
)

// installForwarders replaces tun2socks' TCP/UDP handlers with equivalents that can refuse IPv6
// flows. The refusal has to happen at the SYN (RST before the handshake completes): once the
// userland stack has accepted the connection the app believes it is connected, and a browser
// that "won" the race on IPv6 will not fall back to IPv4 after a later reset.
func installForwarders(s *stack.Stack, allowV6 func() bool) {
	isV6 := func(id stack.TransportEndpointID) bool { return id.LocalAddress.Len() == header.IPv6AddressSize }

	tf := tcp.NewForwarder(s, 0, 2<<10, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		if isV6(id) && !allowV6() {
			r.Complete(true) // RST: connect fails instantly, Happy Eyeballs moves on to IPv4
			return
		}
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			r.Complete(true) // prevent a half-open connection leak
			return
		}
		defer r.Complete(false)
		setTCPOptions(s, ep)
		tunnel.T().HandleTCP(&tcpConn{TCPConn: gonet.NewTCPConn(&wq, ep), id: id})
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tf.HandlePacket)

	uf := udp.NewForwarder(s, func(r *udp.ForwarderRequest) bool {
		id := r.ID()
		if isV6(id) && !allowV6() {
			return false // unhandled: the stack answers ICMP port unreachable, so QUIC falls back fast
		}
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			return false
		}
		tunnel.T().HandleUDP(&udpConn{UDPConn: gonet.NewUDPConn(&wq, ep), id: id})
		return true
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, uf.HandlePacket)
}

type tcpConn struct {
	*gonet.TCPConn
	id stack.TransportEndpointID
}

func (c *tcpConn) ID() stack.TransportEndpointID { return c.id }

type udpConn struct {
	*gonet.UDPConn
	id stack.TransportEndpointID
}

func (c *udpConn) ID() stack.TransportEndpointID { return c.id }

// setTCPOptions mirrors tun2socks' defaults: keepalive plus the stack's default buffer sizes.
func setTCPOptions(s *stack.Stack, ep tcpip.Endpoint) {
	ep.SocketOptions().SetKeepAlive(true)
	idle := tcpip.KeepaliveIdleOption(60 * time.Second)
	ep.SetSockOpt(&idle)
	interval := tcpip.KeepaliveIntervalOption(30 * time.Second)
	ep.SetSockOpt(&interval)
	ep.SetSockOptInt(tcpip.KeepaliveCountOption, 9)

	var ss tcpip.TCPSendBufferSizeRangeOption
	if err := s.TransportProtocolOption(header.TCPProtocolNumber, &ss); err == nil {
		ep.SocketOptions().SetSendBufferSize(int64(ss.Default), false)
	}
	var rs tcpip.TCPReceiveBufferSizeRangeOption
	if err := s.TransportProtocolOption(header.TCPProtocolNumber, &rs); err == nil {
		ep.SocketOptions().SetReceiveBufferSize(int64(rs.Default), false)
	}
}

// keepReading keeps a download going after the app has finished sending. Once one direction ends,
// tun2socks gives the other 60 s through SetReadDeadline (its half-close timeout), so an app that sent
// its request and half-closed, as nc and some download tools do, lost the download a minute later.
// The server still ends the connection as usual.
type keepReading struct{ net.Conn }

func (keepReading) SetReadDeadline(time.Time) error { return nil }

func (c keepReading) CloseRead() error {
	if cr, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return cr.CloseRead()
	}
	return nil
}

func (c keepReading) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
