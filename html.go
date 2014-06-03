package main

import (
  "net/http"
  "html/template"
)

const htmlTemplate = `
<!DOCTYPE html>
<html lang="ja">
<meta charset="utf-8">
<title>プロキシについて</title>
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
<link href="//netdna.bootstrapcdn.com/bootstrap/3.1.1/css/bootstrap.min.css" rel="stylesheet">
<script src="//ajax.googleapis.com/ajax/libs/jquery/1.11.1/jquery.min.js"></script>
<script src="//netdna.bootstrapcdn.com/bootstrap/3.1.1/js/bootstrap.min.js"></script>
<body>
  <div class="container">
    <h1>プロキシについて</h1>

    <p>このプロキシを使うには、自動構成スクリプトとして <a href="http://{{.IPAddress}}:{{.Port}}/proxy.pac" target="_blank">http://{{.IPAddress}}:{{.Port}}/proxy.pac</a> を登録してください。</p>
    <p><button type="button" class="btn btn-primary" onclick="location.href='/reload';return false">設定をリロード</button></p>

    <h2>現在使用しているプロキシ</h2>
    <p>現在以下のプロキシを使用しています。</p>
    <table class="table table-bordered">
      <tbody>
        <tr>
          <th>設定名</th>
          <td>
            {{.Config.Proxy.Name}}
          </td>
        </tr>
        <tr>
          <th>ホスト名</th>
          <td>
            {{.Config.Proxy.Host}}:{{.Config.Proxy.HTTPPort}}<small class="text-muted">(HTTP)</small><br>
            {{.Config.Proxy.Host}}:{{.Config.Proxy.SOCKSPort}}<small class="text-muted">(SOCKS)</small>
          </td>
        </tr>
        <tr>
          <th>ユーザー名</th>
          <td>{{.Config.Proxy.Username}}</td>
        </tr>
        <tr>
          <th>パスワード</th>
          <td>{{.Config.Proxy.Password}}</td>
        </tr>
      </tbody>
    </table>

    <h2>リバースプロキシマッピング</h2>
    <p>マップ元に接続するとプロキシ設定なしで直接目的の場所に接続できます。</p>
    <table class="table table-bordered table-hover">
      <thead>
        <tr>
          <th>マップ元</th>
          <th>接続先</th>
        </tr>
      </thead>
      <tbody>
        {{$ipaddr := .IPAddress}}
        {{range .Config.ReverseMap}}
          <tr>
            <td>{{$ipaddr}}:{{.Port}}</td>
            <td>{{.ConnectTo}}</td>
          </tr>
        {{else}}
          <tr>
            <td colspan="2">現在有効なマッピング設定はありません。</td>
          </tr>
        {{end}}
      </tbody>
    </table>

    <h2>プロキシ除外設定</h2>
    <p>以下のドメインに対する接続はプロキシを経由せず直接接続します。</p>
    <ul>
      {{range $k, $v := .Config.DirectHosts}}
        <li>{{$k}}</tr>
      {{end}}
    </ul>
  </div>
</body>
</html>
`

// serveStat はプロキシサーバの設定情報を Web ページとして返す。
func (rl *relay) serveStat(w http.ResponseWriter, r *http.Request) {
	tpl, err := template.New("").Parse(htmlTemplate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = tpl.Execute(w, map[string]interface{}{
		"Config":    rl.cfg,
		"IPAddress": rl.address,
		"Port":      rl.port,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// reload はプロキシサーバの設定情報を更新する。
func (rl *relay) serveReload(w http.ResponseWriter, r *http.Request) {
	if err := rl.reload(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Connection", "close")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
