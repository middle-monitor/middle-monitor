package workers

import (
	"testing"

	"github.com/gosnmp/gosnmp"
)

// An SNMP agent answers with whatever type the OID is declared as. Only the
// numeric ones can be compared to a threshold; the rest have to come back as
// text so the check records a reading instead of a misleading zero.
func TestSNMPOIDValueSeparatesNumbersFromText(t *testing.T) {
	cases := []struct {
		name        string
		pdu         gosnmp.SnmpPDU
		wantNumeric float64
		wantIsNum   bool
		wantDisplay string
	}{
		{
			name:        "integer",
			pdu:         gosnmp.SnmpPDU{Type: gosnmp.Integer, Value: 42},
			wantNumeric: 42, wantIsNum: true, wantDisplay: "42",
		},
		{
			name:        "uptime ticks",
			pdu:         gosnmp.SnmpPDU{Type: gosnmp.TimeTicks, Value: uint32(12345)},
			wantNumeric: 12345, wantIsNum: true, wantDisplay: "12345",
		},
		{
			name:        "gauge",
			pdu:         gosnmp.SnmpPDU{Type: gosnmp.Gauge32, Value: uint(7)},
			wantNumeric: 7, wantIsNum: true, wantDisplay: "7",
		},
		{
			// A hostname or description is a reading, not a measurement: it must
			// not be reported as the number zero.
			name:        "octet string",
			pdu:         gosnmp.SnmpPDU{Type: gosnmp.OctetString, Value: []byte("router-01")},
			wantNumeric: 0, wantIsNum: false, wantDisplay: "router-01",
		},
		{
			name:        "unknown type",
			pdu:         gosnmp.SnmpPDU{Type: gosnmp.Null, Value: nil},
			wantNumeric: 0, wantIsNum: false, wantDisplay: "<nil>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			numeric, isNum, display := snmpOIDValue(c.pdu)
			if numeric != c.wantNumeric || isNum != c.wantIsNum || display != c.wantDisplay {
				t.Fatalf("got (%v, %v, %q), want (%v, %v, %q)",
					numeric, isNum, display, c.wantNumeric, c.wantIsNum, c.wantDisplay)
			}
		})
	}
}
