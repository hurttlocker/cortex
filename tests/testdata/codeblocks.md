## Setup Instructions

Install Go and set up the environment:

```bash
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin
go install github.com/example/tool@latest
```

## Configuration

The config file uses YAML format:

```yaml
database:
  path: ~/.cortex/cortex.db
  wal: true
```

Make sure to restart after changes.
