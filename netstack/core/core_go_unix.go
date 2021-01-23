// +build linux darwin

package core

import (
	"sync"

	"gvisor.dev/gvisor/pkg/tcpip/buffer"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// Endpoint is ...
type Endpoint struct {
	*channel.Endpoint
	mtu int
	dev Device
	buf []byte
	mu  sync.Mutex
	wt  WriterOffset
}

// NewEndpoint is ...
func NewEndpoint(dev Device, mtu int) stack.LinkEndpoint {
	ep := &Endpoint{
		Endpoint: channel.New(512, uint32(mtu), ""),
		dev:      dev,
		mtu:      mtu,
		buf:      make([]byte, 4+mtu),
		wt:       dev.(WriterOffset),
	}
	ep.Endpoint.AddNotify(ep)
	return ep
}

// Attach is to attach device to stack
func (e *Endpoint) Attach(dispatcher stack.NetworkDispatcher) {
	e.Endpoint.Attach(dispatcher)

	go func(r ReaderOffset, size int, ep *channel.Endpoint) {
		for {
			buf := make([]byte, size)
			n, err := r.ReadOffset(buf, 4)
			if err != nil {
				break
			}
			buf = buf[4 : 4+n]

			switch header.IPVersion(buf) {
			case header.IPv4Version:
				ep.InjectInbound(header.IPv4ProtocolNumber, &stack.PacketBuffer{
					Data: buffer.View(buf).ToVectorisedView(),
				})
			case header.IPv6Version:
				ep.InjectInbound(header.IPv6ProtocolNumber, &stack.PacketBuffer{
					Data: buffer.View(buf).ToVectorisedView(),
				})
			}
		}
	}(e.dev.(ReaderOffset), 4+e.mtu, e.Endpoint)
}

// WriteNotify is to write packets back to system
func (e *Endpoint) WriteNotify() {
	info, ok := e.Endpoint.Read()
	if !ok {
		return
	}

	e.mu.Lock()
	buf := append(e.buf[:4], info.Pkt.NetworkHeader().View()...)
	buf = append(buf, info.Pkt.TransportHeader().View()...)
	buf = append(buf, info.Pkt.Data.ToView()...)
	e.wt.WriteOffset(buf, 4)
	e.mu.Unlock()
}

// ReaderOffset is for unix tun reading with 4 bytes prefix
type ReaderOffset interface {
	ReadOffset([]byte, int) (int, error)
}

// ReaderOffset is for linux tun writing with 4 bytes prefix
type WriterOffset interface {
	WriteOffset([]byte, int) (int, error)
}
