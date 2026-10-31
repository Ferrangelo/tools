// claude-status prints Claude plan usage limits, the same data as /usage in
// Claude Code, without starting a session or a model turn.
//
// It reads the OAuth token that Claude Code stores in .credentials.json and
// sends one GET request to the usage endpoint. It never refreshes the token:
// a refresh rotates the refresh token, and writing it back here could log
// Claude Code out. When the token has expired, run `claude` once to renew it.
#define _DEFAULT_SOURCE
#include <cjson/cJSON.h>
#include <curl/curl.h>
#include <langinfo.h>
#include <locale.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

#define USAGE_URL "https://api.anthropic.com/api/oauth/usage"
#define TIMEOUT_SECONDS 30L
#define BAR_WIDTH 24
#define MAX_ROWS 32

static const char *prog = "claude-status";

static void fail(const char *fmt, const char *arg) {
	fprintf(stderr, "%s: ", prog);
	fprintf(stderr, fmt, arg);
	fputc('\n', stderr);
	exit(1);
}

struct buffer {
	char *data;
	size_t len;
};

static char *read_file(const char *path) {
	FILE *f = fopen(path, "rb");
	if (!f)
		return NULL;
	struct buffer b = {0};
	char chunk[4096];
	size_t n;
	while ((n = fread(chunk, 1, sizeof chunk, f)) > 0) {
		char *grown = realloc(b.data, b.len + n + 1);
		if (!grown)
			fail("out of memory reading %s", path);
		b.data = grown;
		memcpy(b.data + b.len, chunk, n);
		b.len += n;
	}
	int read_error = ferror(f);
	fclose(f);
	if (read_error)
		fail("cannot read %s", path);
	if (!b.data)
		fail("%s is empty", path);
	b.data[b.len] = '\0';
	return b.data;
}

// Returns the Claude Code access token and copies the plan name ("Max",
// "Pro") into plan, or an empty string. Exits with a message when the file,
// the token or its expiry is missing or unusable.
static char *load_token(char *plan, size_t plan_size) {
	char path[4096];
	const char *dir = getenv("CLAUDE_CONFIG_DIR");
	const char *home = getenv("HOME");
	if (dir && *dir)
		snprintf(path, sizeof path, "%s/.credentials.json", dir);
	else if (home && *home)
		snprintf(path, sizeof path, "%s/.claude/.credentials.json", home);
	else
		fail("%s", "HOME is not set, so the credentials file cannot be found");

	char *text = read_file(path);
	if (!text)
		fail("cannot open %s. Log in with: claude /login", path);
	cJSON *root = cJSON_Parse(text);
	explicit_bzero(text, strlen(text));
	free(text);
	if (!root)
		fail("%s is not valid JSON", path);

	cJSON *oauth = cJSON_GetObjectItemCaseSensitive(root, "claudeAiOauth");
	cJSON *token = cJSON_GetObjectItemCaseSensitive(oauth, "accessToken");
	if (!cJSON_IsString(token) || !*token->valuestring)
		fail("no Claude.ai login in %s. Log in with: claude /login", path);

	cJSON *expires = cJSON_GetObjectItemCaseSensitive(oauth, "expiresAt");
	if (cJSON_IsNumber(expires) && expires->valuedouble / 1000 <= (double)time(NULL))
		fail("%s", "the access token has expired. Run `claude` once to renew it, then retry");

	cJSON *tier = cJSON_GetObjectItemCaseSensitive(oauth, "subscriptionType");
	snprintf(plan, plan_size, "%s", cJSON_IsString(tier) ? tier->valuestring : "");
	if (plan[0] >= 'a' && plan[0] <= 'z')
		plan[0] -= 'a' - 'A';

	char *copy = strdup(token->valuestring);
	explicit_bzero(token->valuestring, strlen(token->valuestring));
	cJSON_Delete(root);
	if (!copy)
		fail("%s", "out of memory");
	return copy;
}

static size_t collect(char *ptr, size_t size, size_t nmemb, void *userdata) {
	struct buffer *b = userdata;
	size_t n = size * nmemb;
	char *grown = realloc(b->data, b->len + n + 1);
	if (!grown)
		return 0; // Makes curl stop with CURLE_WRITE_ERROR.
	b->data = grown;
	memcpy(b->data + b->len, ptr, n);
	b->len += n;
	b->data[b->len] = '\0';
	return n;
}

static char *fetch_usage(char *token) {
	if (curl_global_init(CURL_GLOBAL_DEFAULT) != CURLE_OK)
		fail("%s", "cannot initialise libcurl");
	CURL *curl = curl_easy_init();
	if (!curl)
		fail("%s", "cannot initialise libcurl");

	char auth[512];
	if ((size_t)snprintf(auth, sizeof auth, "Authorization: Bearer %s", token) >= sizeof auth)
		fail("%s", "the access token is longer than expected");
	explicit_bzero(token, strlen(token));
	struct curl_slist *headers = NULL;
	headers = curl_slist_append(headers, auth);
	headers = curl_slist_append(headers, "anthropic-beta: oauth-2025-04-20");
	headers = curl_slist_append(headers, "Accept: application/json");
	if (!headers)
		fail("%s", "out of memory");

	struct buffer body = {0};
	char error[CURL_ERROR_SIZE] = "";
	curl_easy_setopt(curl, CURLOPT_URL, USAGE_URL);
	curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
	curl_easy_setopt(curl, CURLOPT_USERAGENT, "claude-status/1.0");
	curl_easy_setopt(curl, CURLOPT_TIMEOUT, TIMEOUT_SECONDS);
	curl_easy_setopt(curl, CURLOPT_ACCEPT_ENCODING, "");
	curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, collect);
	curl_easy_setopt(curl, CURLOPT_WRITEDATA, &body);
	curl_easy_setopt(curl, CURLOPT_ERRORBUFFER, error);

	CURLcode rc = curl_easy_perform(curl);
	explicit_bzero(auth, sizeof auth);
	long status = 0;
	curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &status);
	curl_slist_free_all(headers);
	curl_easy_cleanup(curl);
	curl_global_cleanup();

	if (rc != CURLE_OK)
		fail("request failed: %s", *error ? error : curl_easy_strerror(rc));
	if (status == 401)
		fail("%s", "the server rejected the access token (HTTP 401). Run `claude` once to renew it, then retry");
	if (status != 200) {
		fprintf(stderr, "%s: usage request returned HTTP %ld: %.300s\n", prog, status,
		        body.data ? body.data : "(empty body)");
		exit(1);
	}
	if (!body.data)
		fail("%s", "the usage endpoint returned an empty body");
	return body.data;
}

// Output style. Colour is on only for a terminal without NO_COLOR, and the
// bar characters fall back to ASCII when the locale is not UTF-8.
static int use_color, use_utf8;

static const char *sgr(const char *code) { return use_color ? code : ""; }
#define RESET sgr("\033[0m")
#define BOLD sgr("\033[1m")
#define DIM sgr("\033[2m")
#define TRACK sgr("\033[38;5;238m")
#define TRACK_BG sgr("\033[48;5;238m")
#define SEP (use_utf8 ? " \xc2\xb7 " : " - ")

static const char *level_color(double pct) {
	return pct >= 80 ? sgr("\033[31m") : pct >= 50 ? sgr("\033[33m") : sgr("\033[32m");
}

struct row {
	const char *label;
	double pct; // Negative: the row has no bar.
	char detail[256];
};

static struct row rows[MAX_ROWS];
static int nrows;

static struct row *add_row(const char *label, double pct) {
	if (nrows == MAX_ROWS)
		return NULL;
	struct row *r = &rows[nrows++];
	r->label = label;
	r->pct = pct;
	r->detail[0] = '\0';
	return r;
}

static void append(char *buf, size_t size, const char *fmt, ...) {
	size_t len = strlen(buf);
	if (len + 1 >= size)
		return;
	va_list ap;
	va_start(ap, fmt);
	vsnprintf(buf + len, size - len, fmt, ap);
	va_end(ap);
}

// Parses an ISO 8601 time such as "2026-10-31T19:09:59.72+00:00".
// Returns -1 when the text does not match.
static time_t parse_time(const char *s) {
	struct tm tm = {0};
	if (!s || sscanf(s, "%d-%d-%dT%d:%d:%d", &tm.tm_year, &tm.tm_mon, &tm.tm_mday,
	                 &tm.tm_hour, &tm.tm_min, &tm.tm_sec) != 6)
		return -1;
	tm.tm_year -= 1900;
	tm.tm_mon -= 1;
	long offset = 0;
	const char *zone = strpbrk(strchr(s, 'T'), "Z+-");
	if (zone && *zone != 'Z') {
		int hh = 0, mm = 0;
		if (sscanf(zone + 1, "%d:%d", &hh, &mm) < 1)
			return -1;
		offset = (hh * 3600L + mm * 60L) * (*zone == '-' ? -1 : 1);
	}
	// Round to the nearest minute: the server sends times like 19:09:59.7.
	return timegm(&tm) - offset + (tm.tm_sec >= 30 ? 60 - tm.tm_sec : -tm.tm_sec);
}

// Appends "resets in 4h 15m · Sat Oct 31 2026, 20:10" in local time.
static void append_reset(char *buf, size_t size, const char *iso) {
	time_t at = parse_time(iso);
	if (at < 0) {
		append(buf, size, "%sreset time unknown%s", DIM, RESET);
		return;
	}
	time_t now = time(NULL);
	long left = (long)(at - now);
	if (left <= 0) {
		append(buf, size, "resets now");
		return;
	}
	long d = left / 86400, h = left % 86400 / 3600, m = left % 3600 / 60;
	if (d > 0)
		append(buf, size, "resets in %ldd %ldh", d, h);
	else if (h > 0)
		append(buf, size, "resets in %ldh %ldm", h, m);
	else
		append(buf, size, "resets in %ldm", m > 0 ? m : 1);

	char when[32];
	strftime(when, sizeof when, "%a %b %d %Y, %H:%M", localtime(&at));
	append(buf, size, "%s%s%s%s", DIM, SEP, when, RESET);
}

// Draws a solid bar in eighth-cell steps: full blocks in the usage colour
// over a dark grey track. The partial cell paints its empty part with the
// track colour as background, so the bar has no gap.
static void print_bar(double pct) {
	static const char *const eighths[] = {"", "\xe2\x96\x8f", "\xe2\x96\x8e", "\xe2\x96\x8d",
	                                      "\xe2\x96\x8c", "\xe2\x96\x8b", "\xe2\x96\x8a", "\xe2\x96\x89"};
	int steps = use_utf8 ? 8 : 1;
	int filled = pct <= 0 ? 0 : pct >= 100 ? BAR_WIDTH * steps : (int)(pct * BAR_WIDTH * steps / 100 + 0.5);
	if (filled == 0 && pct > 0)
		filled = 1;
	const char *full = use_utf8 ? "\xe2\x96\x88" : "#"; // █
	const char *empty = !use_utf8 ? "-" : use_color ? full : "\xe2\x96\x91"; // ░
	fputs(level_color(pct), stdout);
	int cell = 0;
	for (; cell < filled / steps; cell++)
		fputs(full, stdout);
	if (filled % steps) {
		printf("%s%s", TRACK_BG, eighths[filled % steps]);
		cell++;
	}
	printf("%s%s", RESET, TRACK);
	for (; cell < BAR_WIDTH; cell++)
		fputs(empty, stdout);
	fputs(RESET, stdout);
}

static const char *label_for(const char *key) {
	static const char *const names[][2] = {
		{"five_hour", "Current session"},
		{"seven_day", "Current week"},
		{"seven_day_opus", "Week, Opus"},
		{"seven_day_sonnet", "Week, Sonnet"},
		{"seven_day_oauth_apps", "Week, OAuth apps"},
		{"seven_day_cowork", "Week, Cowork"},
		// /usage does not show this limit. The label is the user's reading
		// of it: the dollar credits for cloud sessions.
		{"iguana_necktie", "Cloud credits"},
	};
	for (size_t i = 0; i < sizeof names / sizeof names[0]; i++)
		if (strcmp(key, names[i][0]) == 0)
			return names[i][1];
	return key; // Unknown limits keep the server's name.
}

static double number(const cJSON *obj, const char *key, double fallback) {
	const cJSON *item = cJSON_GetObjectItemCaseSensitive(obj, key);
	return cJSON_IsNumber(item) ? item->valuedouble : fallback;
}

// Adds a row for every top-level object with a numeric "utilization", so
// limits the server adds later still appear.
static void add_limits(const cJSON *root) {
	const cJSON *item;
	cJSON_ArrayForEach(item, root) {
		if (!cJSON_IsObject(item) || strcmp(item->string, "extra_usage") == 0)
			continue;
		const cJSON *util = cJSON_GetObjectItemCaseSensitive(item, "utilization");
		if (!cJSON_IsNumber(util))
			continue;
		struct row *r = add_row(label_for(item->string), util->valuedouble);
		if (!r)
			return;
		double spent = number(item, "used_dollars", -1), cap = number(item, "limit_dollars", -1);
		if (spent >= 0 && cap > 0)
			append(r->detail, sizeof r->detail, "$%.2f of $%.2f%s", spent, cap, SEP);
		const cJSON *reset = cJSON_GetObjectItemCaseSensitive(item, "resets_at");
		append_reset(r->detail, sizeof r->detail, cJSON_IsString(reset) ? reset->valuestring : NULL);
		const cJSON *locked = cJSON_GetObjectItemCaseSensitive(item, "locked_reason");
		if (cJSON_IsString(locked))
			append(r->detail, sizeof r->detail, "%s%slocked: %s%s", SEP,
			       sgr("\033[1;31m"), locked->valuestring, RESET);
	}
}

static void add_extra_usage(const cJSON *root) {
	const cJSON *extra = cJSON_GetObjectItemCaseSensitive(root, "extra_usage");
	if (!cJSON_IsObject(extra))
		return;
	struct row *r;
	if (!cJSON_IsTrue(cJSON_GetObjectItemCaseSensitive(extra, "is_enabled"))) {
		if ((r = add_row("Usage credits", -1)))
			append(r->detail, sizeof r->detail, "%soff%s", DIM, RESET);
		return;
	}
	double scale = 1;
	for (int i = (int)number(extra, "decimal_places", 0); i > 0; i--)
		scale *= 10;
	const cJSON *currency = cJSON_GetObjectItemCaseSensitive(extra, "currency");
	int usd = cJSON_IsString(currency) && strcmp(currency->valuestring, "USD") == 0;
	const char *prefix = usd ? "$" : "", *suffix = "";
	if (!usd && cJSON_IsString(currency))
		suffix = currency->valuestring;
	double used = number(extra, "used_credits", 0) / scale;
	double cap = number(extra, "monthly_limit", -1);
	if (!(r = add_row("Usage credits", cap >= 0 ? number(extra, "utilization", 0) : -1)))
		return;
	if (cap >= 0)
		append(r->detail, sizeof r->detail, "%s%.2f of %s%.2f %s%sthis month%s", prefix, used,
		       prefix, cap / scale, suffix, DIM, RESET);
	else
		append(r->detail, sizeof r->detail, "%s%.2f %s%sthis month, no limit%s", prefix, used,
		       suffix, DIM, RESET);
}

static void add_breakdown(const cJSON *root) {
	const cJSON *breakdown = cJSON_GetObjectItemCaseSensitive(root, "seven_day_breakdown");
	const cJSON *list = cJSON_GetObjectItemCaseSensitive(breakdown, "rows");
	const cJSON *item;
	struct row *r = NULL;
	cJSON_ArrayForEach(item, list) {
		const cJSON *name = cJSON_GetObjectItemCaseSensitive(item, "display_name");
		double pct = number(item, "percent", 0);
		if (!cJSON_IsString(name) || pct <= 0)
			continue;
		if (!r && !(r = add_row("Week by product", -2)))
			return;
		append(r->detail, sizeof r->detail, "%s%s %s%.0f%%%s", r->detail[0] ? SEP : "",
		       name->valuestring, BOLD, pct, RESET);
	}
}

static void render(const char *plan) {
	int width = 0;
	for (int i = 0; i < nrows; i++) {
		int len = (int)strlen(rows[i].label);
		if (len > width)
			width = len;
	}
	printf("%sClaude usage%s", BOLD, RESET);
	if (*plan)
		printf("%s%s%s plan%s", DIM, SEP, plan, RESET);
	puts("\n");
	if (nrows == 0) {
		puts("No usage limits returned. Check your login with: claude /login");
		return;
	}
	for (int i = 0; i < nrows; i++) {
		const struct row *r = &rows[i];
		if (r->pct == -2) // Section without a bar, set apart by a blank line.
			putchar('\n');
		printf("  %-*s  ", width, r->label);
		if (r->pct >= 0) {
			print_bar(r->pct);
			if (r->pct > 0 && r->pct < 1)
				printf("  %s%s <1%%%s   ", BOLD, level_color(r->pct), RESET);
			else
				printf("  %s%s%3.0f%%%s   ", BOLD, level_color(r->pct), r->pct, RESET);
		} else if (r->pct == -1) {
			printf("%*s", BAR_WIDTH + 9, "");
		}
		printf("%s\n", r->detail);
	}
}

int main(int argc, char **argv) {
	int raw = 0;
	for (int i = 1; i < argc; i++) {
		if (strcmp(argv[i], "--json") == 0) {
			raw = 1;
		} else if (strcmp(argv[i], "-h") == 0 || strcmp(argv[i], "--help") == 0) {
			printf("Usage: %s [--json]\n\n"
			       "Prints Claude plan usage limits, like /usage in Claude Code.\n"
			       "  --json  print the raw response from the usage endpoint\n\n"
			       "Set NO_COLOR=1 to turn off colours.\n", prog);
			return 0;
		} else {
			fail("unexpected argument: %s (try --help)", argv[i]);
		}
	}

	setlocale(LC_CTYPE, "");
	use_utf8 = strcmp(nl_langinfo(CODESET), "UTF-8") == 0;
	const char *no_color = getenv("NO_COLOR"), *term = getenv("TERM");
	use_color = isatty(STDOUT_FILENO) && !(no_color && *no_color) &&
	            !(term && strcmp(term, "dumb") == 0);

	char plan[32];
	char *token = load_token(plan, sizeof plan);
	char *body = fetch_usage(token);
	free(token);

	cJSON *root = cJSON_Parse(body);
	if (!root && raw) {
		puts(body);
		return 0;
	}
	if (!root)
		fail("%s", "the usage endpoint returned invalid JSON. Run with --json to see it");
	if (raw) {
		char *pretty = cJSON_Print(root);
		puts(pretty ? pretty : body);
		free(pretty);
	} else {
		add_limits(root);
		add_extra_usage(root);
		add_breakdown(root);
		render(plan);
	}
	cJSON_Delete(root);
	free(body);
	return 0;
}
