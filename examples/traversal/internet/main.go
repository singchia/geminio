package main

import "flag"

var (
	tunnel   *string
	internet *string
)

func main() {
	tunnel = flag.String("tunnel", "", "tunnel address to listen for intranet")
	internet = flag.String("internet", "", "internet address to listen and proxy to tunnel")
	flag.Parse()
}
