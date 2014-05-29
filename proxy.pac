// プロキシ自動設定スクリプト
// http://en.wikipedia.org/wiki/Proxy_auto-config

// 以下の部分は実行時に置換される
var ip_addr = "{{.IPAddress}}";
var base_port = parseInt("{{.BasePort}}", 10);
var num_ports = parseInt("{{.NumPorts}}", 10);

// プロキシ一覧を組み立てる
var proxies = [];
for (var i = base_port; i < base_port+num_ports; ++i) {
  proxies.push(ip_addr+":"+i);
}

// プロキシ除外ホスト名一覧
var direct_hosts = {
  "ajax.googleapis.com": 1,
  "ajax.aspnetcdn.com": 1,
  "netdna.bootstrapcdn.com": 1,
  "cdnjs.cloudflare.com": 1,
  "api.bootswatch.com": 1,
  "docs.google.com": 1
};

function shuffle(a) {
    var i = a.length;
    while(i){
        var j = Math.floor(Math.random()*i);
        var t = a[--i];
        a[i] = a[j];
        a[j] = t;
    }
}

// ====================================

function FindProxyForURL(url, host) {
  if (host in direct_hosts) {
    return "DIRECT";
  }

  shuffle(proxies);
  return "PROXY "+proxies.join(";");
}
