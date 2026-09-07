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
	"time"
)

const (
	wireMagic   uint32 = 0x4e334450 // "N3DP"
	wireVersion uint16 = 1
	headerLen          = 24
	maxQFI             = 63
	maxKeyLen          = 64

	MessageHello         uint16 = 1
	MessageSessionUpsert uint16 = 2
	MessageSessionDelete uint16 = 3
	MessageChildSAUpsert uint16 = 4
	MessageChildSADelete uint16 = 5
	MessageStatsGet      uint16 = 6
	MessageACK           uint16 = 0x8000
	MessageStats         uint16 = 0x8001
)

const (
	RoleWriter   uint8 = 1
	RoleObserver uint8 = 2
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

// ChildSA describes one bidirectional ESP SA pair. Algorithm identifiers are
// IKEv2 transform IDs and keys are named from the N3IWF dataplane direction.
type ChildSA struct {
	UEID                  uint64
	PDUSessionID          uint32
	InboundSPI            uint32
	OutboundSPI           uint32
	EncryptionID          uint16
	IntegrityID           uint16
	LocalPort             uint16
	PeerPort              uint16
	ReplayWindow          uint32
	NATT                  bool
	ESN                   bool
	OutboundSequence      uint64
	SoftLifetimeSeconds   uint64
	HardLifetimeSeconds   uint64
	LocalAddress          net.IP
	PeerAddress           net.IP
	LocalSelector         net.IP
	PeerSelector          net.IP
	IPProtocol            uint8
	InboundEncryptionKey  []byte
	InboundIntegrityKey   []byte
	OutboundEncryptionKey []byte
	OutboundIntegrityKey  []byte
}

type ACK struct {
	Status            Status
	Detail            uint32
	AppliedGeneration uint64
}

type Stats struct {
	UplinkPackets       uint64
	DownlinkPackets     uint64
	UnknownTEID         uint64
	UnknownQFI          uint64
	MalformedPackets    uint64
	ReplayDrops         uint64
	CryptoFailures      uint64
	FragmentDrops       uint64
	StaleUpdates        uint64
	ControlToCP         uint64
	ControlFromCP       uint64
	ControlPuntDrops    uint64
	AccessMACLearns     uint64
	AccessMACChanges    uint64
	AccessNeighborDrops uint64
	ActiveSessions      uint64
	ActiveChildSAs      uint64
	UnknownSPI          uint64
	OversizeDrops       uint64
	BufferDrops         uint64
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
	_, err := c.roundTrip(ctx, MessageHello, 0, []byte{RoleWriter, 0, 0, 0})
	return err
}

func (c *Client) HelloObserver(ctx context.Context) error {
	_, err := c.roundTrip(ctx, MessageHello, 0, []byte{RoleObserver, 0, 0, 0})
	return err
}

func (c *Client) UpsertChildSA(ctx context.Context, generation uint64, sa ChildSA) error {
	payload, err := marshalChildSA(sa)
	if err != nil {
		return err
	}
	defer clearBytes(payload)
	_, err = c.roundTrip(ctx, MessageChildSAUpsert, generation, payload)
	return err
}

func (c *Client) DeleteChildSA(ctx context.Context, generation, ueID uint64,
	pduSessionID, inboundSPI uint32) error {
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[0:8], ueID)
	binary.BigEndian.PutUint32(payload[8:12], pduSessionID)
	binary.BigEndian.PutUint32(payload[12:16], inboundSPI)
	_, err := c.roundTrip(ctx, MessageChildSADelete, generation, payload)
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

func (c *Client) GetStats(ctx context.Context) (Stats, error) {
	response, xid, err := c.exchange(ctx, MessageStatsGet, 0, nil)
	if err != nil {
		return Stats{}, err
	}
	return parseStats(response, xid)
}

func (c *Client) roundTrip(
	ctx context.Context,
	messageType uint16,
	generation uint64,
	payload []byte,
) (ACK, error) {
	response, xid, err := c.exchange(ctx, messageType, generation, payload)
	if err != nil {
		return ACK{}, err
	}
	return parseACK(response, xid)
}

func (c *Client) exchange(ctx context.Context, messageType uint16,
	generation uint64, payload []byte) ([]byte, uint32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.connect(); err != nil {
		return nil, 0, err
	}
	c.xid++
	xid := c.xid
	request := marshalMessage(messageType, xid, generation, payload)
	defer clearBytes(request)
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetDeadline(deadline); err != nil {
			return nil, 0, err
		}
	} else if err := c.conn.SetDeadline(time.Time{}); err != nil {
		return nil, 0, err
	}
	if _, err := c.conn.Write(request); err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return nil, 0, err
	}

	response := make([]byte, 2048)
	n, err := c.conn.Read(response)
	if err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return nil, 0, err
	}
	return response[:n], xid, nil
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
	if session.PDUSessionID == 0 ||
		session.UplinkTEID == 0 || session.DownlinkTEID == 0 {
		return nil, errors.New("UE, PDU session and TEID identifiers must be non-zero")
	}
	if len(session.QFIs) == 0 || len(session.QFIs) > maxQFI {
		return nil, errors.New("wire version 1 requires 1..63 active QFIs")
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

func marshalChildSA(sa ChildSA) ([]byte, error) {
	if sa.InboundSPI == 0 || sa.OutboundSPI == 0 || sa.EncryptionID == 0 ||
		sa.ReplayWindow == 0 || sa.IPProtocol == 0 {
		return nil, errors.New("Child SA SPI, encryption, replay window and protocol are required")
	}
	family, local, err := encodeIP(sa.LocalAddress)
	if err != nil {
		return nil, fmt.Errorf("local address: %w", err)
	}
	_, peer, err := encodeIPFamily(sa.PeerAddress, family)
	if err != nil {
		return nil, fmt.Errorf("peer address: %w", err)
	}
	_, localSelector, err := encodeIPFamily(sa.LocalSelector, family)
	if err != nil {
		return nil, fmt.Errorf("local selector: %w", err)
	}
	_, peerSelector, err := encodeIPFamily(sa.PeerSelector, family)
	if err != nil {
		return nil, fmt.Errorf("peer selector: %w", err)
	}
	keys := [][]byte{sa.InboundEncryptionKey, sa.InboundIntegrityKey,
		sa.OutboundEncryptionKey, sa.OutboundIntegrityKey}
	if len(keys[0]) == 0 || len(keys[2]) == 0 {
		return nil, errors.New("inbound and outbound encryption keys are required")
	}
	for _, key := range keys {
		if len(key) > maxKeyLen {
			return nil, fmt.Errorf("Child SA key exceeds %d bytes", maxKeyLen)
		}
	}
	if sa.IntegrityID == 0 && (len(keys[1]) != 0 || len(keys[3]) != 0) {
		return nil, errors.New("integrity keys require a non-zero integrity transform")
	}
	if sa.IntegrityID != 0 && (len(keys[1]) == 0 || len(keys[3]) == 0) {
		return nil, errors.New("integrity transform requires directional integrity keys")
	}

	// Must exactly match struct n3iwf_dp_child_sa_wire (388 bytes).
	payload := make([]byte, 132+(4*maxKeyLen))
	binary.BigEndian.PutUint64(payload[0:8], sa.UEID)
	binary.BigEndian.PutUint32(payload[8:12], sa.PDUSessionID)
	binary.BigEndian.PutUint32(payload[12:16], sa.InboundSPI)
	binary.BigEndian.PutUint32(payload[16:20], sa.OutboundSPI)
	binary.BigEndian.PutUint16(payload[20:22], sa.EncryptionID)
	binary.BigEndian.PutUint16(payload[22:24], sa.IntegrityID)
	binary.BigEndian.PutUint16(payload[24:26], sa.LocalPort)
	binary.BigEndian.PutUint16(payload[26:28], sa.PeerPort)
	binary.BigEndian.PutUint32(payload[28:32], sa.ReplayWindow)
	var flags uint32
	if sa.NATT {
		flags |= 1
	}
	if sa.ESN {
		flags |= 2
	}
	binary.BigEndian.PutUint32(payload[32:36], flags)
	binary.BigEndian.PutUint64(payload[36:44], sa.OutboundSequence)
	binary.BigEndian.PutUint64(payload[44:52], sa.SoftLifetimeSeconds)
	binary.BigEndian.PutUint64(payload[52:60], sa.HardLifetimeSeconds)
	payload[60] = family
	payload[61] = sa.IPProtocol
	payload[62] = uint8(len(keys[0]))
	payload[63] = uint8(len(keys[1]))
	payload[64] = uint8(len(keys[2]))
	payload[65] = uint8(len(keys[3]))
	copy(payload[68:84], local)
	copy(payload[84:100], peer)
	copy(payload[100:116], localSelector)
	copy(payload[116:132], peerSelector)
	copy(payload[132:196], keys[0])
	copy(payload[196:260], keys[1])
	copy(payload[260:324], keys[2])
	copy(payload[324:388], keys[3])
	return payload, nil
}

func clearBytes(data []byte) {
	for index := range data {
		data[index] = 0
	}
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

func parseStats(message []byte, expectedXID uint32) (Stats, error) {
	const legacyStatsPayloadLen = 9 * 8
	const controlStatsPayloadLen = 12 * 8
	const statsPayloadLen = 15 * 8
	const stateStatsPayloadLen = 17 * 8
	const ipsecStatsPayloadLen = 18 * 8
	const mtuStatsPayloadLen = 19 * 8
	const bufferStatsPayloadLen = 20 * 8
	if len(message) != headerLen+legacyStatsPayloadLen &&
		len(message) != headerLen+controlStatsPayloadLen &&
		len(message) != headerLen+statsPayloadLen &&
		len(message) != headerLen+stateStatsPayloadLen &&
		len(message) != headerLen+ipsecStatsPayloadLen &&
		len(message) != headerLen+mtuStatsPayloadLen &&
		len(message) != headerLen+bufferStatsPayloadLen {
		return Stats{}, fmt.Errorf("invalid dataplane stats length %d", len(message))
	}
	if binary.BigEndian.Uint32(message[0:4]) != wireMagic ||
		binary.BigEndian.Uint16(message[4:6]) != wireVersion ||
		binary.BigEndian.Uint16(message[6:8]) != MessageStats {
		return Stats{}, errors.New("invalid dataplane stats response")
	}
	if int(binary.BigEndian.Uint32(message[8:12])) != len(message) ||
		binary.BigEndian.Uint32(message[12:16]) != expectedXID {
		return Stats{}, errors.New("invalid dataplane stats response identity")
	}
	payload := message[headerLen:]
	stats := Stats{
		UplinkPackets: binary.BigEndian.Uint64(payload[0:8]), DownlinkPackets: binary.BigEndian.Uint64(payload[8:16]),
		UnknownTEID: binary.BigEndian.Uint64(payload[16:24]), UnknownQFI: binary.BigEndian.Uint64(payload[24:32]),
		MalformedPackets: binary.BigEndian.Uint64(payload[32:40]), ReplayDrops: binary.BigEndian.Uint64(payload[40:48]),
		CryptoFailures: binary.BigEndian.Uint64(payload[48:56]), FragmentDrops: binary.BigEndian.Uint64(payload[56:64]),
		StaleUpdates: binary.BigEndian.Uint64(payload[64:72]),
	}
	if len(payload) >= controlStatsPayloadLen {
		stats.ControlToCP = binary.BigEndian.Uint64(payload[72:80])
		stats.ControlFromCP = binary.BigEndian.Uint64(payload[80:88])
		stats.ControlPuntDrops = binary.BigEndian.Uint64(payload[88:96])
	}
	if len(payload) >= statsPayloadLen {
		stats.AccessMACLearns = binary.BigEndian.Uint64(payload[96:104])
		stats.AccessMACChanges = binary.BigEndian.Uint64(payload[104:112])
		stats.AccessNeighborDrops = binary.BigEndian.Uint64(payload[112:120])
	}
	if len(payload) >= stateStatsPayloadLen {
		stats.ActiveSessions = binary.BigEndian.Uint64(payload[120:128])
		stats.ActiveChildSAs = binary.BigEndian.Uint64(payload[128:136])
	}
	if len(payload) >= ipsecStatsPayloadLen {
		stats.UnknownSPI = binary.BigEndian.Uint64(payload[136:144])
	}
	if len(payload) >= mtuStatsPayloadLen {
		stats.OversizeDrops = binary.BigEndian.Uint64(payload[144:152])
	}
	if len(payload) == bufferStatsPayloadLen {
		stats.BufferDrops = binary.BigEndian.Uint64(payload[152:160])
	}
	return stats, nil
}
