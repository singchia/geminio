module github.com/singchia/geminio

go 1.17

replace (
	github.com/jumboframes/armorigo => ../../singchia/armorigo
	github.com/singchia/yafsm => ../../austin/yafsm
)

require (
	github.com/jumboframes/armorigo v0.1.0
	github.com/singchia/go-timer/v2 v2.2.0
	github.com/singchia/yafsm v0.99.0
)
