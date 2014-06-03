// package config は proxy-relay の設定情報を管理するパッケージ。
package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config は設定情報を管理するための構造体。
type Config struct {
	Proxy       *Proxy              // proxy-relay が接続しに行くプロキシサーバの設定。
	ReverseMap  map[int]string      // 特定のホストの特定のポート番号に接続するリバースプロキシ設定のリスト。
	DirectHosts map[string]struct{} // プロキシを使わずに接続するホスト名の一覧。
}

// Proxy はプロキシひとつひとつの設定情報を管理するための構造体。
type Proxy struct {
	Name      string
	Host      string
	HTTPPort  int `toml:"http_port"`
	SOCKSPort int `toml:"socks_port"`
	Username  string
	Password  string
}

// New は TOML ファイルを開き、中から設定情報を読み出し適切な形に分解して返す。
func New(tomlfile string) (*Config, error) {
	var cfg struct {
		UseProxy    string `toml:"use_proxy"`
		Reverse     []string
		DirectHosts []string `toml:"direct_hosts"`
		Proxies     map[string]*Proxy
	}
	if _, err := toml.DecodeFile(tomlfile, &cfg); err != nil {
		return nil, err
	}

	var r Config

	// cfg.Reverse に格納された "41000->example.com:8080" のような表記のデータを分解する
	r.ReverseMap = make(map[int]string)
	for _, mapping := range cfg.Reverse {
		kv := strings.SplitN(mapping, "->", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("could not parse mapping setting: %s", mapping)
		}

		p, err := strconv.Atoi(kv[0])
		if err != nil {
			return nil, fmt.Errorf("invalid portnumber: %v", err)
		}
		r.ReverseMap[p] = kv[1]
	}

	px, ok := cfg.Proxies[cfg.UseProxy]
	if !ok {
		return nil, fmt.Errorf("proxy setting not found: %s", cfg.UseProxy)
	}
	r.Proxy = px
	px.Name = cfg.UseProxy

	r.DirectHosts = make(map[string]struct{})
	for _, domain := range cfg.DirectHosts {
		r.DirectHosts[domain] = struct{}{}
	}

	return &r, nil
}
