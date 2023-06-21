module github.com/singchia/geminio

go 1.17

replace (
	github.com/singchia/yafsm => ../../austin/yafsm
	github.com/jumboframes/armorigo => ../../austin/armorigo
	github.com/singchia/go-timer/v2 => ../../austin/go-timer
)

require (
	github.com/jumboframes/armorigo v0.1.0
	github.com/singchia/go-timer/v2 v2.0.3
	github.com/singchia/yafsm v0.99.0
	github.com/sirupsen/logrus v1.9.3
)

require golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
