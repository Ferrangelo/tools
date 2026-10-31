# Builds every tool in this repo and installs it into $(PREFIX)/bin.
# To add a tool: create <name>/, add <name> to TOOLS, and add a build rule
# below if the tool needs compiling. Scripts with a shebang need no rule.
PREFIX ?= $(HOME)/.local
TOOLS := claude-status codex-status

all: $(TOOLS)

claude-status:
	$(MAKE) -C claude-status

codex-status:
	cd codex-status && go build -trimpath -ldflags="-s -w" -o codex-status .

install: all
	for t in $(TOOLS); do install -Dm755 $$t/$$t $(PREFIX)/bin/$$t; done

clean:
	$(MAKE) -C claude-status clean
	rm -f codex-status/codex-status

.PHONY: all install clean $(TOOLS)
