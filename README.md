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
