// randint prints one random integer from an inclusive range to stdout.
// With no arguments it uses 0 through 10. With two arguments it uses those
// bounds in either order. Any 64-bit signed bounds work, including the full
// range. Invalid usage exits 1 with a message on stderr.
//
// Random bits come from the kernel through getrandom(2), and rejection
// sampling maps them onto the range without bias.
#define _DEFAULT_SOURCE
#include <ctype.h>
#include <errno.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/random.h>

#define DEFAULT_LOW 0
#define DEFAULT_HIGH 10
#define SIGN_BIT ((uint64_t)1 << 63)

static const char *prog = "randint";

// Parses one decimal bound. Exits 1 when the text is not a whole int64.
static int64_t parse_int(const char *arg) {
	char *end;
	// strtoll skips leading whitespace; reject it so " 5" fails like "5 ".
	if (*arg == '\0' || isspace((unsigned char)*arg)) {
		goto invalid;
	}
	errno = 0;
	long long v = strtoll(arg, &end, 10);
	if (errno != 0 || *end != '\0') {
		goto invalid;
	}
	return (int64_t)v;
invalid:
	fprintf(stderr, "%s: '%s' is not a valid 64-bit integer\n", prog, arg);
	exit(1);
}

// Returns 64 random bits from the kernel. Exits 1 if getrandom fails.
static uint64_t random_u64(void) {
	uint64_t v;
	unsigned char *p = (unsigned char *)&v;
	size_t got = 0;
	while (got < sizeof v) {
		ssize_t n = getrandom(p + got, sizeof v - got, 0);
		if (n < 0) {
			if (errno == EINTR) {
				continue;
			}
			fprintf(stderr, "%s: getrandom: %s\n", prog, strerror(errno));
			exit(1);
		}
		got += (size_t)n;
	}
	return v;
}

// Returns a uniform value in [0, span]. span may be UINT64_MAX.
static uint64_t random_upto(uint64_t span) {
	if (span == UINT64_MAX) {
		return random_u64();
	}
	uint64_t n = span + 1;
	// 2^64 mod n draws at the bottom would make low results more likely,
	// so reject them. (-n) % n computes 2^64 mod n in 64-bit arithmetic.
	uint64_t threshold = -n % n;
	uint64_t r;
	do {
		r = random_u64();
	} while (r < threshold);
	return r % n;
}

// Signed and unsigned conversions below go through an offset encoding:
// adding 2^63 maps INT64_MIN..INT64_MAX onto 0..UINT64_MAX in order. This
// keeps all arithmetic in uint64_t, where wraparound is well defined, and
// avoids implementation-defined unsigned-to-signed conversions.
static uint64_t to_offset(int64_t v) {
	return (uint64_t)v + SIGN_BIT;
}

static int64_t from_offset(uint64_t u) {
	if (u >= SIGN_BIT) {
		return (int64_t)(u - SIGN_BIT);
	}
	return (int64_t)u - INT64_MAX - 1;
}

int main(int argc, char **argv) {
	int64_t low = DEFAULT_LOW;
	int64_t high = DEFAULT_HIGH;

	if (argc == 3) {
		low = parse_int(argv[1]);
		high = parse_int(argv[2]);
	} else if (argc != 1) {
		fprintf(stderr,
		        "%s: expected 0 or 2 integer arguments, got %d\n"
		        "usage: randint [<int1> <int2>]\n",
		        prog, argc - 1);
		return 1;
	}

	if (low > high) {
		int64_t t = low;
		low = high;
		high = t;
	}

	uint64_t base = to_offset(low);
	uint64_t span = to_offset(high) - base;
	printf("%" PRId64 "\n", from_offset(base + random_upto(span)));
	return 0;
}
