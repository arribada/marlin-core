// Copyright (c) The Arribada initiative.
// Licensed under the MIT License.

/*
This program should be the equivalent of the ruleset below.

	table ip filter {
		chain FORWARD {
			type filter hook forward priority filter; policy accept;
			iifname "eth0" oifname "wlan0" accept
			iifname "wlan0" oifname "eth0" ct state related,established accept
		}
	}

	table ip nat {
		chain POSTROUTING {
			type nat hook postrouting priority srcnat; policy accept;
			oifname "wlan0" masquerade
		}
	}
*/
package main

import (
	"flag"
	"log"

	//	"github.com/davecgh/go-spew/spew"
	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
)

var (
	flagLan string
	flagWan string
)

func init() {
	flag.StringVar(&flagLan, "lan", "eth0", "interface on the subnet side")
	flag.StringVar(&flagWan, "wan", "wlan0", "interface on the internet side")
}

func main() {
	// TODO: add a test to validate against the wanted ruleset.
	// TODO: do we need/want to wait for TS to have added their rules first?

	flag.Parse()
	if flagLan == "" {
		log.Fatal("empty lan interface")
	}
	if flagWan == "" {
		log.Fatal("empty wan interface")
	}

	conn, err := nftables.New()
	if err != nil {
		log.Fatal(err)
	}

	forwardEgress(conn)
	forwardIngress(conn)
	masquerade(conn)
	if err := conn.Flush(); err != nil {
		log.Fatal(err)
	}
}

func showRules(conn *nftables.Conn) {
	ch, err := conn.ListChains()
	if err != nil {
		log.Fatal(err)
	}

	tbl, err := conn.ListTables()
	if err != nil {
		log.Fatal(err)
	}

	for _, v := range tbl {
		if v.Name != "filter" && v.Name != "nat" {
			continue
		}
		println(v.Name)
		for _, w := range ch {
			if !((v.Name == "filter" && w.Name == "FORWARD") || (v.Name == "nat" && w.Name == "POSTROUTING")) {
				continue
			}
			println(w.Name)
			_, err := conn.GetRules(v, w)
			if err != nil {
				log.Println(err.Error())
				continue
			}
			// fmt.Printf("%v\n", rl)
			// spew.Dump(rl)
		}
	}
}

func forwardEgress(conn *nftables.Conn) {
	filter := conn.AddTable(
		&nftables.Table{
			Name:   "filter",
			Family: nftables.TableFamilyIPv4,
		})

	pol := nftables.ChainPolicy(nftables.ChainPolicyAccept)
	forward := conn.AddChain(&nftables.Chain{
		Name:     "FORWARD",
		Table:    filter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Type:     nftables.ChainTypeFilter,
		Policy:   &pol,
	})

	lan := make([]byte, 16)
	copy(lan, []byte(flagLan))
	wan := make([]byte, 16)
	copy(wan, []byte(flagWan))
	conn.AddRule(&nftables.Rule{
		Table: filter,
		Chain: forward,
		Exprs: []expr.Any{
			&expr.Meta{
				Key:            expr.MetaKeyIIFNAME,
				SourceRegister: false,
				Register:       1,
			},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     lan,
			},
			&expr.Meta{
				Key:            expr.MetaKeyOIFNAME,
				SourceRegister: false,
				Register:       1,
			},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     wan,
			},
			&expr.Verdict{
				Kind: expr.VerdictAccept,
			},
		},
	})
}

func forwardIngress(conn *nftables.Conn) {
	filter := conn.AddTable(
		&nftables.Table{
			Name:   "filter",
			Family: nftables.TableFamilyIPv4,
		})

	pol := nftables.ChainPolicy(nftables.ChainPolicyAccept)
	forward := conn.AddChain(&nftables.Chain{
		Name:     "FORWARD",
		Table:    filter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Type:     nftables.ChainTypeFilter,
		Policy:   &pol,
	})

	lan := make([]byte, 16)
	copy(lan, []byte(flagLan))
	wan := make([]byte, 16)
	copy(wan, []byte(flagWan))
	conn.AddRule(&nftables.Rule{
		Table: filter,
		Chain: forward,
		Exprs: []expr.Any{
			&expr.Meta{
				Key:            expr.MetaKeyIIFNAME,
				SourceRegister: false,
				Register:       1,
			},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     wan,
			},
			&expr.Meta{
				Key:            expr.MetaKeyOIFNAME,
				SourceRegister: false,
				Register:       1,
			},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     lan,
			},
			&expr.Ct{
				Register:       1,
				SourceRegister: false,
				Key:            expr.CtKeySTATE,
				Direction:      0,
			},
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            4,
				Mask:           binaryutil.NativeEndian.PutUint32(expr.CtStateBitRELATED | expr.CtStateBitESTABLISHED),
				Xor:            binaryutil.NativeEndian.PutUint32(0),
			},
			&expr.Cmp{
				Op:       expr.CmpOpNeq,
				Register: 1,
				Data:     make([]byte, 4),
			},
			&expr.Verdict{
				Kind: expr.VerdictAccept,
			},
		},
	})
}

func masquerade(conn *nftables.Conn) {
	nat := conn.AddTable(
		&nftables.Table{
			Name:   "nat",
			Family: nftables.TableFamilyIPv4,
		})

	pol := nftables.ChainPolicy(nftables.ChainPolicyAccept)
	postrouting := conn.AddChain(&nftables.Chain{
		Name:     "POSTROUTING",
		Table:    nat,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityNATSource,
		Type:     nftables.ChainTypeNAT,
		Policy:   &pol,
	})

	wan := make([]byte, 16)
	copy(wan, []byte(flagWan))
	conn.AddRule(&nftables.Rule{
		Table: nat,
		Chain: postrouting,
		Exprs: []expr.Any{
			&expr.Meta{
				Key:            expr.MetaKeyOIFNAME,
				SourceRegister: false,
				Register:       1,
			},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     wan,
			},
			&expr.Masq{
				Random:      false,
				FullyRandom: false,
				Persistent:  false,
				ToPorts:     false,
				RegProtoMin: 0,
				RegProtoMax: 0,
			},
		},
	})
}
