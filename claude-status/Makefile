CC ?= cc
CFLAGS ?= -O2 -pipe
CFLAGS += -std=c11 -Wall -Wextra -Wpedantic $(shell pkg-config --cflags libcurl libcjson)
LDLIBS += $(shell pkg-config --libs libcurl libcjson)
PREFIX ?= $(HOME)/.local

claude-status: main.c
	$(CC) $(CFLAGS) $(LDFLAGS) -s -o $@ $< $(LDLIBS)

install: claude-status
	install -Dm755 claude-status $(PREFIX)/bin/claude-status

clean:
	rm -f claude-status

.PHONY: install clean
