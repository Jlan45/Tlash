package mihomoapi

import "time"

type Version struct {
	Meta    bool   `json:"meta"`
	Version string `json:"version"`
}

// Config is the subset of GET /configs that Tlash displays.
type Config struct {
	Mode        string `json:"mode"`
	LogLevel    string `json:"log-level"`
	MixedPort   int    `json:"mixed-port"`
	Port        int    `json:"port"`
	SocksPort   int    `json:"socks-port"`
	RedirPort   int    `json:"redir-port"`
	TProxyPort  int    `json:"tproxy-port"`
	AllowLan    bool   `json:"allow-lan"`
	IPv6        bool   `json:"ipv6"`
	BindAddress string `json:"bind-address"`
	Tun         struct {
		Enable bool   `json:"enable"`
		Stack  string `json:"stack"`
		Device string `json:"device"`
	} `json:"tun"`
}

type DelayHistory struct {
	Time  time.Time `json:"time"`
	Delay int       `json:"delay"`
}

// Proxy is a single outbound or a proxy group (groups have All set).
type Proxy struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	UDP      bool           `json:"udp"`
	Alive    bool           `json:"alive"`
	History  []DelayHistory `json:"history"`
	Now      string         `json:"now"`
	All      []string       `json:"all"`
	Hidden   bool           `json:"hidden"`
	Icon     string         `json:"icon"`
	TestURL  string         `json:"testUrl"`
	Fixed    string         `json:"fixed"`
	Provider string         `json:"provider-name"`
}

func (p Proxy) IsGroup() bool { return p.All != nil }

// LastDelay returns the most recent delay in ms; 0 means failed or never tested.
func (p Proxy) LastDelay() (delay int, tested bool) {
	if len(p.History) == 0 {
		return 0, false
	}
	return p.History[len(p.History)-1].Delay, true
}

type Traffic struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	UpTotal   int64 `json:"upTotal"`
	DownTotal int64 `json:"downTotal"`
}

type Memory struct {
	InUse   int64 `json:"inuse"`
	OSLimit int64 `json:"oslimit"`
}

type LogEntry struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

type ConnectionMetadata struct {
	Network         string `json:"network"`
	Type            string `json:"type"`
	SourceIP        string `json:"sourceIP"`
	SourcePort      string `json:"sourcePort"`
	DestinationIP   string `json:"destinationIP"`
	DestinationPort string `json:"destinationPort"`
	Host            string `json:"host"`
	Process         string `json:"process"`
	SniffHost       string `json:"sniffHost"`
}

type Connection struct {
	ID          string             `json:"id"`
	Metadata    ConnectionMetadata `json:"metadata"`
	Upload      int64              `json:"upload"`
	Download    int64              `json:"download"`
	Start       time.Time          `json:"start"`
	Chains      []string           `json:"chains"`
	Rule        string             `json:"rule"`
	RulePayload string             `json:"rulePayload"`
}

type ConnectionsSnapshot struct {
	DownloadTotal int64        `json:"downloadTotal"`
	UploadTotal   int64        `json:"uploadTotal"`
	Memory        int64        `json:"memory"`
	Connections   []Connection `json:"connections"`
}
