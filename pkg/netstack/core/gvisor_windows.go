//go:build windows

package core

import (
	"errors"
	"io"
	"log"
	"sync"
	"unsafe"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// Device is a tun-like device for reading packets from system
type Device interface {
	// Writer is ...
	io.Writer
	// DeviceType is ...
	// give device type
	DeviceType() string
}

// Endpoint is ...
type Endpoint struct {
	// Endpoint is ...
	*channel.Endpoint
	// Device is ...
	Device Device
	// Writer is ...
	Writer io.Writer

	mtu  int
	mu   sync.Mutex
	buff []byte
}

// NewEndpoint is ...
func NewEndpoint(dev Device, mtu int) stack.LinkEndpoint {
	wt, ok := dev.(io.Writer)
	if !ok {
		log.Panic(errors.New("not a valid device for windows"))
	}
	ep := &Endpoint{
		Endpoint: channel.New(512, uint32(mtu), ""),
		Device:   dev,
		Writer:   wt,
		mtu:      mtu,
		buff:     make([]byte, mtu),
	}
	ep.Endpoint.AddNotify(ep)
	return ep
}

// Attach is to attach device to stack
func (e *Endpoint) Attach(dispatcher stack.NetworkDispatcher) {
	e.Endpoint.Attach(dispatcher)

	// WinDivert has no Reader
	r, ok := e.Device.(Reader)
	if !ok {
		wt, ok := e.Device.(io.WriterTo)
		if !ok {
			log.Panic(errors.New("not a valid device for windows"))
		}
		go func(w io.Writer, wt io.WriterTo) {
			if _, err := wt.WriteTo(w); err != nil {
				return
			}
		}((*endpoint)(unsafe.Pointer(e.Endpoint)), wt)
		return
	}
	// WinTun
	go func(r Reader, size int, ep *channel.Endpoint) {
		for {
			buf := make([]byte, size)
			nr, err := r.Read(buf, 0)
			if err != nil {
				break
			}
			buf = buf[:nr]

			pktBuffer := stack.NewPacketBuffer(stack.PacketBufferOptions{
				ReserveHeaderBytes: 0,
				Payload:            buffer.MakeWithData(buf),
			})
			switch header.IPVersion(buf) {
			case header.IPv4Version:
				ep.InjectInbound(header.IPv4ProtocolNumber, pktBuffer)
			case header.IPv6Version:
				ep.InjectInbound(header.IPv6ProtocolNumber, pktBuffer)
			}
			pktBuffer.DecRef()
		}
	}(r, e.mtu+4, e.Endpoint)
}

// WriteNotify is to write packets back to system
func (e *Endpoint) WriteNotify() {
	pkt := e.Endpoint.Read()

	e.mu.Lock()
	buf := append(e.buff[:0], pkt.NetworkHeader().View().AsSlice()...)
	buf = append(buf, pkt.TransportHeader().View().AsSlice()...)
	vv := pkt.Data().ToBuffer()
	buf = append(buf, vv.Flatten()...)
	e.Writer.Write(buf)
	e.mu.Unlock()
}

// endpoint is for WinDivert
// write packets from WinDivert to gvisor netstack
type endpoint struct {
	Endpoint channel.Endpoint
}

// Write is to write packet to stack
func (e *endpoint) Write(b []byte) (int, error) {
	buf := append(make([]byte, 0, len(b)), b...)

	switch header.IPVersion(buf) {
	case header.IPv4Version:
		// WinDivert: need to calculate chekcsum
		pkt := header.IPv4(buf)
		pkt.SetChecksum(0)
		pkt.SetChecksum(^pkt.CalculateChecksum())
		switch ProtocolNumber := pkt.TransportProtocol(); ProtocolNumber {
		case header.UDPProtocolNumber:
			hdr := header.UDP(pkt.Payload())
			sum := header.PseudoHeaderChecksum(ProtocolNumber, pkt.DestinationAddress(), pkt.SourceAddress(), hdr.Length())
			sum = checksum.Checksum(hdr.Payload(), sum)
			hdr.SetChecksum(0)
			hdr.SetChecksum(^hdr.CalculateChecksum(sum))
		case header.TCPProtocolNumber:
			hdr := header.TCP(pkt.Payload())
			sum := header.PseudoHeaderChecksum(ProtocolNumber, pkt.DestinationAddress(), pkt.SourceAddress(), pkt.PayloadLength())
			sum = checksum.Checksum(hdr.Payload(), sum)
			hdr.SetChecksum(0)
			hdr.SetChecksum(^hdr.CalculateChecksum(sum))
		}
		pktBuffer := stack.NewPacketBuffer(stack.PacketBufferOptions{
			ReserveHeaderBytes: 0,
			Payload:            buffer.MakeWithData(buf),
		})
		e.Endpoint.InjectInbound(header.IPv4ProtocolNumber, pktBuffer)
		pktBuffer.DecRef()
	case header.IPv6Version:
		// WinDivert: need to calculate chekcsum
		pkt := header.IPv6(buf)
		switch ProtocolNumber := pkt.TransportProtocol(); ProtocolNumber {
		case header.UDPProtocolNumber:
			hdr := header.UDP(pkt.Payload())
			sum := header.PseudoHeaderChecksum(ProtocolNumber, pkt.DestinationAddress(), pkt.SourceAddress(), hdr.Length())
			sum = checksum.Checksum(hdr.Payload(), sum)
			hdr.SetChecksum(0)
			hdr.SetChecksum(^hdr.CalculateChecksum(sum))
		case header.TCPProtocolNumber:
			hdr := header.TCP(pkt.Payload())
			sum := header.PseudoHeaderChecksum(ProtocolNumber, pkt.DestinationAddress(), pkt.SourceAddress(), pkt.PayloadLength())
			sum = checksum.Checksum(hdr.Payload(), sum)
			hdr.SetChecksum(0)
			hdr.SetChecksum(^hdr.CalculateChecksum(sum))
		}
		pktBuffer := stack.NewPacketBuffer(stack.PacketBufferOptions{
			ReserveHeaderBytes: 0,
			Payload:            buffer.MakeWithData(buf),
		})
		e.Endpoint.InjectInbound(header.IPv6ProtocolNumber, pktBuffer)
		pktBuffer.DecRef()
	}
	return len(buf), nil
}
