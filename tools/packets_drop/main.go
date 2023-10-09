package main

import (
	"context"
	"flag"
	"log"

	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/go-xtables"
	"github.com/singchia/go-xtables/iptables"
	"github.com/singchia/go-xtables/pkg/network"
)

func main() {
	port := flag.Int("dport", 1202, "dst port packets to drop")
	limit := flag.Int("limit", 1024, "packet limit per second, default 1024 unlimited")
	burst := flag.Int("burst", 2048, "packet burst per second, default 2048")
	flag.Parse()

	ipt := iptables.NewIPTables().Table(iptables.TableTypeFilter).
		Chain(iptables.ChainTypeINPUT).MatchProtocol(false, network.ProtocolTCP).
		MatchTCP(iptables.WithMatchTCPDstPort(false, *port)).
		MatchLimit(iptables.WithMatchLimit(xtables.Rate{Rate: *limit, Unit: xtables.Second}),
			iptables.WithMatchLimitBurst(*burst)).TargetDrop()

	err := ipt.Insert()
	if err != nil {
		log.Printf("iptables insert err: %s", err)
		return
	}

	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())

	err = ipt.Delete()
	if err != nil {
		log.Printf("iptables delete err: %s", err)
		return
	}
}
