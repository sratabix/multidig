# multidig

See how far a DNS change has propagated.

## Install

Grab a binary from the [releases](https://github.com/sratabix/multidig/releases).

## Usage

```sh
multidig example.com
multidig -t A,AAAA,MX example.com
multidig --expect 203.0.113.10 -w 10s example.com
multidig -c EU,NA -n 10 example.com
multidig -s 8.8.8.8,1.1.1.1 example.com
multidig --format json example.com | jq .
```

## Development

`make check` runs everything CI runs. `make dist` builds the release archives.

```sh
make check
make dist
```
