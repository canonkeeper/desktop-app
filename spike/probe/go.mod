module github.com/canonkeeper/desktop-app/spike/probe

go 1.25.0

require (
	github.com/canonkeeper/desktop-app/spike/translate v0.0.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/shirou/gopsutil/v4 v4.25.6
	golang.org/x/sys v0.28.0
)

require (
	github.com/coder/websocket v1.8.15 // indirect
	github.com/ebitengine/purego v0.8.4 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/power-devops/perfstat v0.0.0-20210106213030-5aafc221ea8c // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/canonkeeper/desktop-app/spike/translate => ../translate
