package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/go-xtables"
	"github.com/singchia/go-xtables/iptables"
	"github.com/singchia/go-xtables/pkg/network"
)

// this must be compiled in linux.
func main() {
	port := flag.Int("dport", 1202, "dst port packets to drop")
	limit := flag.Int("limit", 1024, "packet limit per second, default 1024 unlimited")
	burst := flag.Int("burst", 2048, "packet burst per second, default 2048")
	wait := flag.Int("time", 10, "drop time in second, default 10s, -1 means unlimited")
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

	if *wait == -1 {
		sig := sigaction.NewSignal()
		sig.Wait(context.TODO())

		err = ipt.Delete()
		if err != nil {
			log.Printf("iptables delete err: %s", err)
			return
		}
	}

	tick := time.NewTicker(time.Duration(*wait) * time.Second)
	<-tick.C
	err = ipt.Delete()
	if err != nil {
		log.Printf("iptables delete err: %s", err)
		return
	}
}
