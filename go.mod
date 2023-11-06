module github.com/singchia/geminio

go 1.17

replace github.com/singchia/go-timer/v2 => ../../austin/go-timer

replace github.com/jumboframes/armorigo => ../../singchia/armorigo

replace github.com/singchia/yafsm => ../../austin/yafsm



require (
	github.com/golang/mock v1.6.0
	github.com/jumboframes/armorigo v0.2.3
	github.com/singchia/go-timer/v2 v2.2.0
	github.com/singchia/go-xtables v1.0.0
	github.com/singchia/yafsm v1.0.0
)

require (
	github.com/singchia/go-hammer v0.0.2-0.20220516141917-9d83fc02d653 // indirect
	golang.org/x/crypto v0.5.0 // indirect
	golang.org/x/sys v0.6.0 // indirect
)
