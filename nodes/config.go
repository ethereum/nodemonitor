package nodes

type Config struct {
	ReloadInterval string
	ServerAddress  string
	Clients        []ClientInfo
	Metrics        metricsConfig

	InfuraKey      string
	InfuraEndpoint string

	AlchemyKey      string
	AlchemyEndpoint string

	EtherscanKey      string
	EtherscanEndpoint string
}

type metricsConfig struct {
	Enabled   bool
	Endpoint  string
	Username  string
	Database  string
	Password  string
	Namespace string
}

type ClientInfo struct {
	Url       string
	Name      string
	Kind      string
	Ratelimit int
}
