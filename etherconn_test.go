// etherconn_test

/*
Test setup reuqirements:
  - two interfaces name specified by argument testifA and testifB, these two interfaces are connected together
*/
package etherconn

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/hujun-open/myaddr"
	"github.com/vishvananda/netlink"
)

const (
	testifA = "A"
	testifB = "B"
)

func testCreateVETHLink(a, b string) (netlink.Link, netlink.Link, error) {
	linka := new(netlink.Veth)
	linka.Name = a
	linka.PeerName = b
	netlink.LinkDel(linka)
	err := netlink.LinkAdd(linka)
	if err != nil {
		return nil, nil, err
	}
	linkb, err := netlink.LinkByName(b)
	if err != nil {
		return nil, nil, err
	}
	err = netlink.LinkSetUp(linka)
	if err != nil {
		return nil, nil, err
	}
	err = netlink.LinkSetUp(linkb)
	if err != nil {
		return nil, nil, err
	}
	return linka, linkb, nil
}

type testEtherConnEndpoint struct {
	mac               net.HardwareAddr
	vlans             []*VLAN
	ETypes            []uint16
	defaultConn       bool
	defaultConnMirror bool
	dstMACFlag        int
	recvMulticast     bool
	filter            string
}

type testEtherConnSingleCase struct {
	A          testEtherConnEndpoint
	B          testEtherConnEndpoint
	C          testEtherConnEndpoint //used only in testing default mirroring
	shouldFail bool
}

type testRUDPConnSingleCase struct {
	AEther     testEtherConnEndpoint
	BEther     testEtherConnEndpoint
	AIP        net.IP
	APort      int
	BIP        net.IP
	BPort      int
	shouldFail bool
}

// testGenDummyIPbytes return a dummy IP packet slice
func testGenDummyIPbytes(length int, v4 bool) []byte {
	payload := make([]byte, length)
	rand.Read(payload)
	buf := gopacket.NewSerializeBuffer()
	var iplayer gopacket.SerializableLayer
	udplayer := &layers.UDP{
		SrcPort: layers.UDPPort(3333),
		DstPort: layers.UDPPort(4444),
	}
	if v4 {
		srcip := make([]byte, 4)
		rand.Read(srcip)
		dstip := make([]byte, 4)
		rand.Read(dstip)
		iplayer = &layers.IPv4{
			Version:  4,
			SrcIP:    net.IP(srcip),
			DstIP:    net.IP(dstip),
			Protocol: layers.IPProtocol(17),
			TTL:      16,
		}
		udplayer.SetNetworkLayerForChecksum(iplayer.(*layers.IPv4))
	} else {
		srcip := make([]byte, 16)
		rand.Read(srcip)
		dstip := make([]byte, 16)
		rand.Read(dstip)
		iplayer = &layers.IPv6{
			Version:    6,
			SrcIP:      net.IP(srcip),
			DstIP:      net.IP(dstip),
			NextHeader: layers.IPProtocol(17),
			HopLimit:   16,
		}
		udplayer.SetNetworkLayerForChecksum(iplayer.(*layers.IPv6))
	}
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	gopacket.SerializeLayers(buf, opts,
		iplayer,
		udplayer,
		gopacket.Payload(payload))
	return buf.Bytes()

}

const (
	macTestCorrect = iota
	macTestBD
	macTestWrong
)

func TestEtherConn(t *testing.T) {
	testCaseList := []testEtherConnSingleCase{
		//good case, no Q
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac:   net.HardwareAddr{0x14, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{},
			},
			B: testEtherConnEndpoint{
				mac:   net.HardwareAddr{0x14, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{},
			},
		},
		//good case, dot1q
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x14, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			B: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x14, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
		},
		//good case, qinq
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x14, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
					&VLAN{
						ID:        222,
						EtherType: 0x8100,
					},
				},
			},
			B: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x14, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
					&VLAN{
						ID:        222,
						EtherType: 0x8100,
					},
				},
			},
		},
		//negtive case, blocked by filter
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x14, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
					&VLAN{
						ID:        222,
						EtherType: 0x8100,
					},
				},
			},
			B: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x14, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
					&VLAN{
						ID:        222,
						EtherType: 0x8100,
					},
				},
				filter: "vlan 333",
			},
			shouldFail: true,
		},

		//negative case, different vlan
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			B: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        101,
						EtherType: 0x8100,
					},
				},
			},
			shouldFail: true,
		},

		//negative case, wrong mac
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				dstMACFlag: macTestWrong,
			},
			B: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			shouldFail: true,
		},

		//send to broadcast good case, even recv has wrong vlan id
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				dstMACFlag: macTestBD,
			},
			B: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        101,
						EtherType: 0x8100,
					},
				},
				recvMulticast: true,
			},
			shouldFail: false,
		},

		//send to broadcast negative case, recv doesn't accept multicast
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				dstMACFlag: macTestBD,
			},
			B: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        101,
						EtherType: 0x8100,
					},
				},
				recvMulticast: false,
			},
			shouldFail: true,
		},

		//default receive case, no mirroring, no matching vlan&mac
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				dstMACFlag: macTestWrong,
			},
			B: testEtherConnEndpoint{
				defaultConn: true,
				mac:         net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        101,
						EtherType: 0x8100,
					},
				},
				recvMulticast: false,
			},
		},
		//default receive case, mirroring, no matching vlan&mac
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				dstMACFlag: macTestCorrect,
			},
			B: testEtherConnEndpoint{
				defaultConn:       true,
				ETypes:            []uint16{1},
				defaultConnMirror: true,
				mac:               net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        101,
						EtherType: 0x8100,
					},
				},
				recvMulticast: false,
			},
			C: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				recvMulticast: false,
			},
		},
		//negative case, default receive case, no mirroring, no matching vlan&mac
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				dstMACFlag: macTestCorrect,
			},
			B: testEtherConnEndpoint{
				defaultConn:       true,
				ETypes:            []uint16{1},
				defaultConnMirror: false,
				mac:               net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        101,
						EtherType: 0x8100,
					},
				},
				recvMulticast: false,
			},
			C: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				recvMulticast: false,
			},
			shouldFail: true,
		},
		//negative case, ethertypes not allowed
		testEtherConnSingleCase{
			A: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				dstMACFlag: macTestCorrect,
			},
			B: testEtherConnEndpoint{
				ETypes: []uint16{0x1},
				mac:    net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
				recvMulticast: false,
			},
			shouldFail: true,
		},
	}

	testFunc := func(c testEtherConnSingleCase) error {
		_, _, err := testCreateVETHLink(testifA, testifB)
		if err != nil {
			return err
		}
		filterstr := "udp or (vlan and udp)"
		if c.A.filter != "" {
			filterstr = c.A.filter
		}
		mods := []RelayOption{
			WithDebug(true),
			WithBPFFilter(filterstr),
		}
		if c.A.defaultConn {
			mods = append(mods, WithDefaultReceival(c.A.defaultConnMirror))
		}
		peerA, err := NewRawSocketRelay(context.Background(), testifA, mods...)
		if err != nil {
			return err
		}
		defer peerA.Stop()
		filterstr = "udp or (vlan and udp)"
		if c.B.filter != "" {
			filterstr = c.B.filter
		}
		mods = []RelayOption{
			WithDebug(true),
			WithBPFFilter(filterstr),
		}
		if c.B.defaultConn {
			mods = append(mods, WithDefaultReceival(c.B.defaultConnMirror))
		}
		peerB, err := NewRawSocketRelay(context.Background(), testifB, mods...)
		if err != nil {
			return err
		}
		defer peerB.Stop()
		emods := []EtherConnOption{
			WithVLANs(c.A.vlans),
		}
		if len(c.A.ETypes) == 0 {
			emods = append(emods, WithEtherTypes(DefaultEtherTypes))
		} else {
			emods = append(emods, WithEtherTypes(c.A.ETypes))
		}
		if c.A.defaultConn {
			emods = append(emods, WithDefault())
		}
		econnA := NewEtherConn(c.A.mac, peerA, emods...)
		defer econnA.Close()
		emods = []EtherConnOption{
			WithVLANs(c.B.vlans),
			WithRecvMulticast(c.B.recvMulticast),
		}
		if len(c.B.ETypes) == 0 {
			emods = append(emods, WithEtherTypes(DefaultEtherTypes))
		} else {
			emods = append(emods, WithEtherTypes(c.B.ETypes))
		}
		if c.B.defaultConn {
			emods = append(emods, WithDefault())
		}
		econnB := NewEtherConn(c.B.mac, peerB, emods...)
		defer econnB.Close()

		if len(c.C.mac) > 0 {
			t.Logf("create endpoint C")
			emods = []EtherConnOption{
				WithVLANs(c.C.vlans),
				WithRecvMulticast(c.C.recvMulticast),
			}
			if len(c.C.ETypes) == 0 {
				emods = append(emods, WithEtherTypes(DefaultEtherTypes))
			} else {
				emods = append(emods, WithEtherTypes(c.C.ETypes))
			}
			if c.C.defaultConn {
				emods = append(emods, WithDefault())
			}
			econnC := NewEtherConn(c.C.mac, peerB, emods...)
			defer econnC.Close()
		}
		maxSize := 1000
		for i := 0; i < 10; i++ {
			fmt.Printf("send pkt %d\n", i)
			pktSize := maxSize - rand.Intn(maxSize-63)
			p := testGenDummyIPbytes(pktSize, i%2 == 0)
			var dst net.HardwareAddr
			switch c.A.dstMACFlag {
			case macTestCorrect:
				dst = c.B.mac
			case macTestBD:
				dst = BroadCastMAC
			default:
				dst = net.HardwareAddr{0, 0, 0, 0, 0, 0}
			}
			fmt.Printf("send packet with length %d to %v\n content %v\n", len(p), dst, p)
			_, err := econnA.WriteIPPktTo(p, dst)
			if err != nil {
				return err
			}
			rcvdbuf := make([]byte, maxSize+100)
			//set read timeout
			err = econnB.SetReadDeadline(time.Now().Add(3 * time.Second))
			if err != nil {
				return err
			}
			n, _, err := econnB.ReadPktFrom(rcvdbuf)
			if err != nil {
				return err
			}
			if !bytes.Equal(p, rcvdbuf[:n]) {
				return fmt.Errorf("recvied bytes is different from sent for pkt %d, sent %v, recv %v", i, p, rcvdbuf[:n])
			} else {
				fmt.Printf("recved a good  pkt\n")
			}
		}

		return nil
	}
	for i, c := range testCaseList {
		// if i != 10 {
		// 	continue
		// }
		err := testFunc(c)
		if err != nil {
			if c.shouldFail {
				fmt.Printf("case %d failed as expected,%v\n", i, err)
			} else {
				t.Fatalf("case %d failed,%v", i, err)
			}
		} else {
			if c.shouldFail {
				t.Fatalf("case %d succeed but should fail", i)
			}
		}
	}
}

func TestRUDPConn(t *testing.T) {
	testCaseList := []testRUDPConnSingleCase{
		testRUDPConnSingleCase{
			AEther: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			BEther: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			AIP:   net.ParseIP("1.1.1.1"),
			BIP:   net.ParseIP("1.1.1.100"),
			APort: 1999,
			BPort: 2999,
		},

		testRUDPConnSingleCase{
			AEther: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x1},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			BEther: testEtherConnEndpoint{
				mac: net.HardwareAddr{0x12, 0x11, 0x11, 0x11, 0x11, 0x2},
				vlans: []*VLAN{
					&VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			AIP:   net.ParseIP("2001:dead::1"),
			BIP:   net.ParseIP("2001:beef::1"),
			APort: 1999,
			BPort: 2999,
		},
	}

	testFunc := func(c testRUDPConnSingleCase) error {
		_, _, err := testCreateVETHLink(testifA, testifB)
		if err != nil {
			return err
		}
		peerA, err := NewRawSocketRelay(context.Background(), testifA, WithDebug(true))
		if err != nil {
			return err
		}
		defer peerA.Stop()
		peerB, err := NewRawSocketRelay(context.Background(), testifB, WithDebug(true))
		if err != nil {
			return err
		}
		defer peerB.Stop()

		resolvMacFunc := func(net.IP) net.HardwareAddr {
			switch c.AEther.dstMACFlag {
			case macTestCorrect:
				return c.BEther.mac
			case macTestBD:
				return BroadCastMAC
			default:
				return net.HardwareAddr{0, 0, 0, 0, 0, 0}
			}
		}
		econnA := NewEtherConn(c.AEther.mac, peerA, WithVLANs(c.AEther.vlans))
		econnB := NewEtherConn(c.BEther.mac, peerB, WithVLANs(c.BEther.vlans), WithRecvMulticast(c.BEther.recvMulticast))
		rudpA, err := NewRUDPConn(myaddr.GenConnectionAddrStr("", c.AIP, c.APort), econnA, WithResolveNextHopMacFunc(resolvMacFunc))
		if err != nil {
			return err
		}
		rudpB, err := NewRUDPConn(myaddr.GenConnectionAddrStr("", c.BIP, c.BPort), econnB)
		if err != nil {
			return err
		}
		maxSize := 1000
		for i := 0; i < 10; i++ {
			p := testGenDummyIPbytes(maxSize-rand.Intn(maxSize-100), true)
			fmt.Printf("send packet with length %d\n", len(p))
			_, err := rudpA.WriteTo(p, &net.UDPAddr{IP: c.BIP, Zone: "udp", Port: c.BPort})
			if err != nil {
				return err
			}
			rcvdbuf := make([]byte, maxSize+100)
			//set read timeout
			err = rudpB.SetReadDeadline(time.Now().Add(time.Second))
			if err != nil {
				return err
			}
			n, _, err := rudpB.ReadFrom(rcvdbuf)
			if err != nil {
				return err
			}
			if !bytes.Equal(p, rcvdbuf[:n]) {
				return fmt.Errorf("recvied bytes is different from sent")
			}
		}
		return nil
	}
	for i, c := range testCaseList {
		err := testFunc(c)
		if err != nil {
			if c.shouldFail {
				fmt.Printf("case %d failed as expected,%v\n", i, err)
			} else {
				t.Fatalf("case %d failed,%v", i, err)
			}
		} else {
			if c.shouldFail {
				t.Fatalf("case %d succeed but should fail", i)
			}
		}
	}
}

//v is the orignal value, vs is the orignal string
//newIDs is the value for SetIDs, newv is the new VLANs after setIDs
type testVLANsCase struct {
	v          VLANs
	vs         string
	newIDs     []uint16
	newv       VLANs
	shouldFail bool
}

func TestVLANs(t *testing.T) {
	testCaseList := []testVLANsCase{
		testVLANsCase{
			v: VLANs{
				&VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
				&VLAN{
					ID:        200,
					EtherType: 0x8200,
				},
			},
			vs:     "|100|200",
			newIDs: []uint16{111, 222},
			newv: VLANs{
				&VLAN{
					ID:        111,
					EtherType: 0x8100,
				},
				&VLAN{
					ID:        222,
					EtherType: 0x8200,
				},
			},
		},

		testVLANsCase{
			v: VLANs{
				&VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
				&VLAN{
					ID:        200,
					EtherType: 0x8200,
				},
			},
			vs:     "|100|200",
			newIDs: []uint16{111, 222},
			newv: VLANs{
				&VLAN{
					ID:        111,
					EtherType: 0x8100,
				},
				&VLAN{
					ID:        220,
					EtherType: 0x8200,
				},
			},
			shouldFail: true,
		},
	}
	testFunc := func(c testVLANsCase) error {
		if c.v.String() != c.vs {
			return fmt.Errorf("c.v string %v is different from expected %v", c.v.String(), c.vs)
		}
		err := c.v.SetIDs(c.newIDs)
		if err != nil {
			return err
		}
		if !c.newv.Equal(c.v) {
			return fmt.Errorf("c.newv %v is different from expected %v", c.v, c.newv)
		}
		return nil
	}
	for i, c := range testCaseList {
		err := testFunc(c)
		if err != nil {
			if c.shouldFail {
				fmt.Printf("case %d failed as expected,%v\n", i, err)
			} else {
				t.Fatalf("case %d failed,%v", i, err)
			}
		} else {
			if c.shouldFail {
				t.Fatalf("case %d succeed but should fail", i)
			}
		}
	}

}
