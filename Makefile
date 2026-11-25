# Builds every tool in this repo and installs it into $(PREFIX)/bin.
# To add a tool: create <name>/, add <name> to TOOLS, and add a build rule
# below if the tool needs compiling. Scripts with a shebang need no rule.
PREFIX ?= $(HOME)/.local
TOOLS := claude-status codex-status fix-keyring net-check randint rename-by-date softupdate stgz stgzrm tgz tgzrm

all: $(TOOLS)

claude-status:
	$(MAKE) -C claude-status

codex-status:
	cd codex-status && go build -trimpath -ldflags="-s -w" -o codex-status .

net-check:
	cd net-check && go build -trimpath -ldflags="-s -w" -o net-check .

randint:
	$(CC) -O2 -pipe -std=c11 -Wall -Wextra -Wpedantic -s -o randint/randint randint/main.c

install: all
	for t in $(TOOLS); do install -Dm755 $$t/$$t $(PREFIX)/bin/$$t; done

clean:
	$(MAKE) -C claude-status clean
	rm -f codex-status/codex-status
	rm -f net-check/net-check
	rm -f randint/randint

.PHONY: all install clean $(TOOLS)
