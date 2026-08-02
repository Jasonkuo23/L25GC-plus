// Package n3iwfdp implements the versioned control contract between the
// free5GC-derived N3IWF control plane and the ONVM N3IWF dataplane.
package n3iwfdp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

const (
	wireMagic   uint32 = 0x4e334450 // "N3DP"
	wireVersion uint16 = 1
	headerLen          = 24
	maxQFI             = 63

	MessageHello         uint16 = 1
	MessageSessionUpsert uint16 = 2
	MessageSessionDelete uint16 = 3
	MessageChildSAUpsert uint16 = 4
	MessageChildSADelete uint16 = 5
	MessageStatsGet      uint16 = 6
	MessageACK           uint16 = 0x8000
	MessageStats         uint16 = 0x8001
)

type Status uint32

const (
	StatusOK Status = iota
	StatusBadMessage
	StatusUnsupportedVersion
	StatusStaleGeneration
	StatusNotFound
	StatusCapacity
	StatusUnsupported
)

type Session struct {
	UEID            uint64
	PDUSessionID    uint32
	UplinkTEID      uint32
	DownlinkTEID    uint32
	UEPDUAddress    net.IP
	N3IWFNWuAddress net.IP
	UENWuAddress    net.IP
	N3IWFN3Address  net.IP
	UPFN3Address    net.IP
	QFIs            []uint8
}

type ACK struct {
	Status            Status
	Detail            uint32
	AppliedGeneration uint64
}

type Client struct {
	path string

	mu   sync.Mutex
	conn *net.UnixConn
	xid  uint32
}

func New(path string) *Client {
	return &Client{path: path}
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *Client) connect() error {
	if c.conn != nil {
		return nil
	}
	conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{
		Name: c.path,
		Net:  "unixpacket",
	})
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

func (c *Client) Hello(ctx context.Context) error {
	_, err := c.roundTrip(ctx, MessageHello, 0, nil)
	return err
}

func (c *Client) UpsertSession(ctx context.Context, generation uint64, session Session) error {
	payload, err := marshalSession(session)
	if err != nil {
		return err
	}
	_, err = c.roundTrip(ctx, MessageSessionUpsert, generation, payload)
	return err
}

func (c *Client) DeleteSession(
	ctx context.Context,
	generation uint64,
	ueID uint64,
	pduSessionID uint32,
) error {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint64(payload[0:8], ueID)
	binary.BigEndian.PutUint32(payload[8:12], pduSessionID)
	_, err := c.roundTrip(ctx, MessageSessionDelete, generation, payload)
	return err
}

func (c *Client) roundTrip(
	ctx context.Context,
	messageType uint16,
	generation uint64,
	payload []byte,
) (ACK, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.connect(); err != nil {
		return ACK{}, err
	}
	c.xid++
	request := marshalMessage(messageType, c.xid, generation, payload)
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetDeadline(deadline); err != nil {
			return ACK{}, err
		}
	}
	if _, err := c.conn.Write(request); err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return ACK{}, err
	}

	response := make([]byte, 2048)
	n, err := c.conn.Read(response)
	if err != nil {
		return ACK{}, err
	}
	return parseACK(response[:n], c.xid)
}

func marshalMessage(messageType uint16, xid uint32, generation uint64, payload []byte) []byte {
	message := make([]byte, headerLen+len(payload))
	binary.BigEndian.PutUint32(message[0:4], wireMagic)
	binary.BigEndian.PutUint16(message[4:6], wireVersion)
	binary.BigEndian.PutUint16(message[6:8], messageType)
	binary.BigEndian.PutUint32(message[8:12], uint32(len(message)))
	binary.BigEndian.PutUint32(message[12:16], xid)
	binary.BigEndian.PutUint64(message[16:24], generation)
	copy(message[headerLen:], payload)
	return message
}

func marshalSession(session Session) ([]byte, error) {
	if session.UEID == 0 || session.PDUSessionID == 0 ||
		session.UplinkTEID == 0 || session.DownlinkTEID == 0 {
		return nil, errors.New("UE, PDU session and TEID identifiers must be non-zero")
	}
	if len(session.QFIs) != 1 {
		return nil, errors.New("wire version 1 requires exactly one active QFI")
	}

	family, uePDU, err := encodeIP(session.UEPDUAddress)
	if err != nil {
		return nil, fmt.Errorf("UE PDU address: %w", err)
	}
	if family != 4 {
		return nil, errors.New("wire version 1 supports IPv4 sessions only")
	}
	_, n3iwfNWu, err := encodeIPFamily(session.N3IWFNWuAddress, family)
	if err != nil {
		return nil, fmt.Errorf("N3IWF NWu address: %w", err)
	}
	_, ueNWu, err := encodeIPFamily(session.UENWuAddress, family)
	if err != nil {
		return nil, fmt.Errorf("UE NWu address: %w", err)
	}
	_, n3iwfN3, err := encodeIPFamily(session.N3IWFN3Address, family)
	if err != nil {
		return nil, fmt.Errorf("N3IWF N3 address: %w", err)
	}
	_, upfN3, err := encodeIPFamily(session.UPFN3Address, family)
	if err != nil {
		return nil, fmt.Errorf("UPF N3 address: %w", err)
	}

	// Must exactly match struct n3iwf_dp_session_wire.
	payload := make([]byte, 8+4+4+4+1+1+2+(16*5)+maxQFI)
	binary.BigEndian.PutUint64(payload[0:8], session.UEID)
	binary.BigEndian.PutUint32(payload[8:12], session.PDUSessionID)
	binary.BigEndian.PutUint32(payload[12:16], session.UplinkTEID)
	binary.BigEndian.PutUint32(payload[16:20], session.DownlinkTEID)
	payload[20] = family
	payload[21] = uint8(len(session.QFIs))
	copy(payload[24:40], uePDU)
	copy(payload[40:56], n3iwfNWu)
	copy(payload[56:72], ueNWu)
	copy(payload[72:88], n3iwfN3)
	copy(payload[88:104], upfN3)

	seen := uint64(0)
	for index, qfi := range session.QFIs {
		if qfi == 0 || qfi > maxQFI || seen&(uint64(1)<<qfi) != 0 {
			return nil, fmt.Errorf("invalid or duplicate QFI %d", qfi)
		}
		seen |= uint64(1) << qfi
		payload[104+index] = qfi
	}
	return payload, nil
}

func encodeIP(ip net.IP) (uint8, []byte, error) {
	if ipv4 := ip.To4(); ipv4 != nil {
		encoded := make([]byte, 16)
		copy(encoded, ipv4)
		return 4, encoded, nil
	}
	if ipv6 := ip.To16(); ipv6 != nil {
		encoded := make([]byte, 16)
		copy(encoded, ipv6)
		return 6, encoded, nil
	}
	return 0, nil, errors.New("invalid IP address")
}

func encodeIPFamily(ip net.IP, required uint8) (uint8, []byte, error) {
	family, encoded, err := encodeIP(ip)
	if err != nil {
		return 0, nil, err
	}
	if family != required {
		return 0, nil, errors.New("mixed address families are not supported by wire version 1")
	}
	return family, encoded, nil
}

func parseACK(message []byte, expectedXID uint32) (ACK, error) {
	if len(message) < headerLen+16 {
		return ACK{}, io.ErrUnexpectedEOF
	}
	if binary.BigEndian.Uint32(message[0:4]) != wireMagic {
		return ACK{}, errors.New("invalid dataplane response magic")
	}
	if binary.BigEndian.Uint16(message[4:6]) != wireVersion {
		return ACK{}, errors.New("unsupported dataplane response version")
	}
	if binary.BigEndian.Uint16(message[6:8]) != MessageACK {
		return ACK{}, errors.New("unexpected dataplane response type")
	}
	if int(binary.BigEndian.Uint32(message[8:12])) != len(message) {
		return ACK{}, errors.New("invalid dataplane response length")
	}
	if binary.BigEndian.Uint32(message[12:16]) != expectedXID {
		return ACK{}, errors.New("dataplane transaction ID mismatch")
	}
	ack := ACK{
		Status:            Status(binary.BigEndian.Uint32(message[24:28])),
		Detail:            binary.BigEndian.Uint32(message[28:32]),
		AppliedGeneration: binary.BigEndian.Uint64(message[32:40]),
	}
	if ack.Status != StatusOK {
		return ack, fmt.Errorf("dataplane rejected update: status=%d detail=%d",
			ack.Status, ack.Detail)
	}
	return ack, nil
}
