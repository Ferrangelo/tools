# net-check

`net-check` requires Linux and the `ip` command. It uses `ping` when a route
has a gateway; a missing or unsuccessful ping is a warning because routers may
filter ICMP. Direct routes and tunnels do not require a gateway ping.

By default, the command resolves `example.com` through the system resolver and
opens a TCP connection to `1.1.1.1:443`. Name resolution can use hosts files or
caches. TCP success proves only reachability of that endpoint, not HTTPS success
or unrestricted internet access. Failure can be specific to the target provider.
Each diagnostic operation has its own timeout, defaulting to three seconds.

```sh
net-check -timeout 5s -dns-host example.com -target '[2606:4700:4700::1111]:443'
net-check -h
```

Exit status is 0 when local routing, system name resolution, and TCP succeed,
1 when a required diagnostic fails, and 2 for invalid options. Gateway warnings
do not affect the exit status.
