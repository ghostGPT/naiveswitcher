# Naive Switcher

### 文件夹结构

```shell
|- path-to-naiveswitcher
   |- naiveswitcher
   |- naiveproxy-v131.0.6778.86-1-mac-x64
```

### 使用说明

```shell
$ ./naiveswitcher -h
Usage of ./naiveswitcher:
  -a int
    	自动切换到最快服务器的间隔时间（分钟） (default 30)
  -b string
    	启动节点（默认为 naive 节点 https://a:b@domain:port）
  -d	调试模式
  -l string
    	监听端口 (default "0.0.0.0:1080")
  -r string
    	DNS 解析器 IP (default "8.8.4.4:53")
  -s string
    	订阅链接 URL (default "https://example.com/sublink")
  -v	显示版本
  -w string
    	Web 控制台端口 (default "0.0.0.0:1081")
```

### 可选的 UDP-over-Naive

服务端 forwardproxy 启用 `uot` 后，可以让 UDP 通过现有官方 NaiveProxy 的
普通 CONNECT 隧道传输，无需修改或替换 NaiveProxy。当前支持 UoT v2 的单目标模式。

在现有 naiveswitcher 启动参数中增加：

```sh
-udp-listen 127.0.0.1:1082
```

这会新增一个只接受 SOCKS5 UDP ASSOCIATE 的入口；原 TCP 入口不变。
PassWall 的 TCP 节点继续指向原来的 1080，UDP 节点单独配置为 SOCKS5
`127.0.0.1:1082`，不要再选择“与 TCP 相同”。需要 UDP 443 的应用还应移除
PassWall 对该 UDP 端口的丢弃规则。先升级服务端，再调整 UDP 节点。

也可以使用独立入口做临时测试，避免重启现有 TCP 会话：

```sh
go build -o naive-udp ./cmd/naive-udp
./naive-udp -listen 127.0.0.1:1082 -upstream 127.0.0.1:10790
```

`-upstream` 指向已经运行的官方 NaiveProxy SOCKS5 端口，也可以指向
naiveswitcher 的 TCP 转发入口。这个独立工具不会切换节点或下载更新。

如果运营商 UDP QoS 严重，Naive 远端应使用 `https://`，以便本地到服务器这段
走 TLS/TCP。`quic://` 仍然依赖本地 UDP。UoT 不能消除 TCP 丢包重传导致的延迟。

入口默认关闭，启用后默认无 SOCKS 身份认证，建议只监听回环地址交给本机
PassWall 使用。每个入口最多 128 个 SOCKS 关联、256 个 UoT 流，每个关联最多
64 个目标，每个流最多排队 8 个包；拥塞时丢弃新包，避免无限堆积。空包保留，
SOCKS UDP 分片不支持，UDP 来源会绑定到控制连接的 IP 和第一个合法包的源端口。
关联或流空闲两分钟会释放，控制连接关闭会清理其所有流。

同一 UDP 目标始终使用同一个 UoT 流和服务端 UDP socket，保持源端口稳定。
切换 Naive 节点会中断现有流；后续包会重新建立连接。测试期间应固定到已启用
UoT 的服务端，不能让自动切换选到未升级的节点。

```sh
go test -race ./...
go test ./pkg/proxy -run '^$' -bench BenchmarkUDPOverNaive -benchmem
```

### Web 界面

#### 主界面
- <http://localhost:1081/> - 主控制面板，包含服务器状态、日志查看器和控制功能

#### 传统接口（保留）
- <http://localhost:1081/s> - 服务器数量和 IP 列表（规则中的绕过列表）
- <http://localhost:1081/p> - 服务器 ping 状态

#### API 接口

所有 API 接口返回以下格式的 JSON 响应：
```json
{
  "success": true/false,
  "data": {...} or "error": "message"
}
```

**GET** `/api/status` - 获取当前系统状态
```json
{
  "current_server": "https://...",
  "error_count": 0,
  "down_stats": {...},
  "naive_version": "v131.0.6778.86-1",
  "switcher_version": "888.888.888",
  "auto_switch_paused": false,
  "available_servers": [...],
  "uptime": "1h 23m 45s",
  "start_time": 1234567890
}
```

**POST** `/api/switch` - 切换服务器
```json
// 请求体：
{
  "type": "auto|avoid|select",
  "target_server": "https://...",  // 当 type="select" 时使用
  "avoid_server": "https://..."    // 当 type="avoid" 时使用
}
```

**POST** `/api/auto-switch` - 控制自动切换
```json
// 请求体：
{
  "action": "pause|resume"
}
```

**POST** `/api/update` - 触发更新检查

**GET** `/api/logs` - 获取系统日志（纯文本）
