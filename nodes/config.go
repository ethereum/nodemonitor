package nodes

import (
	"net/url"
	"time"
)

type Config struct {
	ReloadInterval tomlDuration
	ServerAddress  string
	Clients        []ClientInfo
}

type ClientInfo struct {
	Url  *tomlUrl
	Name string
}

func (c *ClientInfo) URL() *url.URL {
	if c.Url == nil {
		return nil
	}
	u := url.URL(*c.Url)
	return &u
}

type tomlDuration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler
func (d *tomlDuration) UnmarshalText(data []byte) error {
	duration, err := time.ParseDuration(string(data))
	if err == nil {
		*d = tomlDuration(duration)
	}
	return err
}

// MarshalText implements encoding.TextMarshaler
func (d tomlDuration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

type tomlUrl url.URL

func (t *tomlUrl) UnmarshalText(data []byte) error {
	u, err := url.Parse(string(data))
	if err == nil {
		*t = tomlUrl(*u)
	}
	return err
}

// MarshalText implements encoding.TextMarshaler
func (t *tomlUrl) MarshalText() ([]byte, error) {
	u := url.URL(*t)
	return []byte(u.String()), nil
}
