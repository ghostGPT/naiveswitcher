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

### Web service

- <http://localhost:1081> running status
- <http://localhost:1081/p> server ping status
- <http://localhost:1081/s> server count and ip list (bypass in rule)
- <http://localhost:1081/v> switcher version
