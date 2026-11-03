package n2transport

import "testing"

func TestConfigValidation(t *testing.T) {
	valid := Config{Name: "N2_TEST", LocalIPs: []string{"192.0.2.1"}, PeerIPs: []string{"192.0.2.2"}, Port: 38412}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "bad\x00name", "bad\rname", "bad/name", "abcdefghijklmnopq"} {
		cfg := valid
		cfg.Name = name
		if cfg.Validate() == nil {
			t.Errorf("accepted name %q", name)
		}
	}
	for _, ip := range []string{"0.0.0.0", "::1", "224.0.0.1", "192.0.2.1", "bad"} {
		cfg := valid
		cfg.PeerIPs = []string{ip}
		if cfg.Validate() == nil {
			t.Errorf("accepted %s", ip)
		}
	}
}
func TestUEAssociated(t *testing.T) {
	type identity struct{ Value int64 }
	type value struct {
		AMFUENGAPID *identity
		RANUENGAPID *identity
		NASPDU      []byte
	}
	type ie struct{ Value value }
	type pdu struct{ List []ie }
	if UEAssociated(pdu{[]ie{{value{NASPDU: []byte{1, 2, 3}}}}}) {
		t.Fatal("NAS bytes are not an identity")
	}
	if !UEAssociated(pdu{[]ie{{value{RANUENGAPID: &identity{0}}}}}) {
		t.Fatal("UE ID zero is valid")
	}
}
