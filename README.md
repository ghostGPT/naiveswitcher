# Naive Switcher

### Folder Structure

```shell
|- path-to-naiveswitcher
   |- naiveswitcher
   |- naiveproxy-v131.0.6778.86-1-mac-x64
```

### Usage

```shell
$ ./naiveswitcher -h
Usage of ./naiveswitcher:
  -a int
    	Auto switch fastest duration (minutes) (default 30)
  -b string
    	Bootup node (default naive node https://a:b@domain:port)
  -d	Debug mode
  -l string
    	Listen port (default "0.0.0.0:1080")
  -r string
    	DNS resolver IP (default "8.8.4.4:53")
  -s string
    	Subscribe to a URL (default "https://example.com/sublink")
  -v	Show version
  -w string
    	Web port (default "0.0.0.0:1081")
```

### Web Interface

#### Main Interface
- <http://localhost:1081/> - Main dashboard with server status, logs viewer, and controls

#### Legacy Endpoints (preserved)
- <http://localhost:1081/s> - Server count and IP list (bypass in rule)
- <http://localhost:1081/p> - Server ping status

#### API Endpoints

All API endpoints return JSON responses with the following format:
```json
{
  "success": true/false,
  "data": {...} or "error": "message"
}
```

**GET** `/api/status` - Get current system status
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

**POST** `/api/switch` - Switch server
```json
// Request body:
{
  "type": "auto|avoid|select",
  "target_server": "https://...",  // for type="select"
  "avoid_server": "https://..."    // for type="avoid"
}
```

**POST** `/api/auto-switch` - Control auto-switch
```json
// Request body:
{
  "action": "pause|resume"
}
```

**POST** `/api/update` - Trigger update check

**GET** `/api/logs` - Get system logs (plain text)
