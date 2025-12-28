package common

type Connect struct {
	Dns       string `mapstructure:"dns" json:"dns"`
	Type      string `mapstructure:"type" json:"type"`
	LocalPort int    `mapstructure:"local-port" json:"localPort"`
	LocalHost string `mapstructure:"local-host" json:"localHost"`
	TaskPort  int    `mapstructure:"task-port" json:"taskPort"`
	Secret    string `mapstructure:"secret" json:"secret"`
}
