// wc3lan shim for Warcraft III 1.27b.
//
// The shim is loaded as ijl15.dll: game.dll imports that DLL by ordinal, so
// Windows loads us before any LAN code runs. It rewrites the WS2_32 sendto
// import of game.dll so that every discovery packet the game sends to
// 255.255.255.255:6112 is also sent, from the game's own socket, to the
// unicast addresses of the peers (Tailscale, ZeroTier, other subnets).
//
// Because the fan-out uses the game's socket, the answers arrive at the same
// socket and from the real peer address: the game lists the game and opens
// its TCP session directly to the peer. No proxy, no port rewriting and no
// second process.

#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <windows.h>
#include <iphlpapi.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define LAN_PORT 6112
#define MAX_PEERS 96
#define REFRESH_MS 5000
#define TAILSCALE_EVERY 12
#define PEERS_FILE "wc3lan.peers"
#define LOG_FILE "wc3lan.log"

typedef int(WINAPI *sendto_fn)(SOCKET, const char *, int, int,
                               const struct sockaddr *, int);

static sendto_fn real_sendto;
static CRITICAL_SECTION g_lock;
static LONG g_lock_state;
static CRITICAL_SECTION g_log_lock;
static LONG g_log_state;
static struct in_addr g_peers[MAX_PEERS];
static int g_peer_count;
static volatile LONG g_patched;
static volatile LONG g_logged_bind;
static char g_dir[MAX_PATH];

// ---------------------------------------------------------------- logging --

static void log_line(const char *fmt, ...) {
	HANDLE f;
	char path[MAX_PATH];
	char msg[1024];
	va_list ap;
	int n;

	va_start(ap, fmt);
	n = vsnprintf(msg, sizeof(msg) - 3, fmt, ap);
	va_end(ap);
	if (n < 0) return;
	msg[n++] = '\r';
	msg[n++] = '\n';
	msg[n] = 0;
	if (g_dir[0]) {
		snprintf(path, sizeof(path), "%s%s", g_dir, LOG_FILE);
	} else {
		strcpy(path, LOG_FILE);
	}

	EnterCriticalSection(&g_log_lock);
	f = CreateFileA(path, FILE_APPEND_DATA, FILE_SHARE_READ | FILE_SHARE_WRITE,
	                NULL, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
	if (f != INVALID_HANDLE_VALUE) {
		DWORD written;
		WriteFile(f, msg, (DWORD)n, &written, NULL);
		CloseHandle(f);
	}
	LeaveCriticalSection(&g_log_lock);
}

// ------------------------------------------------------------------ peers --

static int parse_ipv4(const char *s, struct in_addr *out) {
	unsigned a, b, c, d;
	if (sscanf(s, "%u.%u.%u.%u", &a, &b, &c, &d) != 4) return 0;
	if (a > 255 || b > 255 || c > 255 || d > 255) return 0;
	out->s_addr = htonl((a << 24) | (b << 16) | (c << 8) | d);
	return 1;
}

static void add_peer(struct in_addr ip) {
	int i;
	if (ip.s_addr == 0 || ip.s_addr == INADDR_BROADCAST) return;
	for (i = 0; i < g_peer_count; i++) {
		if (g_peers[i].s_addr == ip.s_addr) return;
	}
	if (g_peer_count < MAX_PEERS) {
		g_peers[g_peer_count++] = ip;
	}
}

static void read_peers_file(void) {
	char path[MAX_PATH];
	char line[256];
	FILE *f;

	snprintf(path, sizeof(path), "%s%s", g_dir, PEERS_FILE);
	f = fopen(path, "r");
	if (!f) return;
	while (fgets(line, sizeof(line), f)) {
		struct in_addr ip;
		char *hash = strchr(line, '#');
		if (hash) *hash = 0;
		if (parse_ipv4(line, &ip)) add_peer(ip);
	}
	fclose(f);
}

// run_wait starts a command and waits for it. Output redirection is left to
// cmd.exe: Wine does not connect the pipes of a Unix child process, but it
// does honour "> file", which is what the Tailscale capture needs.
static int run_wait(const char *cmdline) {
	STARTUPINFOA si;
	PROCESS_INFORMATION pi;
	char cmd[1200];
	DWORD code = 1;

	memset(&si, 0, sizeof(si));
	si.cb = sizeof(si);
	si.dwFlags = STARTF_USESHOWWINDOW;
	si.wShowWindow = SW_HIDE;
	snprintf(cmd, sizeof(cmd), "%s", cmdline);
	memset(&pi, 0, sizeof(pi));
	if (!CreateProcessA(NULL, cmd, NULL, NULL, FALSE, CREATE_NO_WINDOW, NULL,
	                    NULL, &si, &pi)) {
		return -1;
	}
	WaitForSingleObject(pi.hProcess, 5000);
	GetExitCodeProcess(pi.hProcess, &code);
	CloseHandle(pi.hThread);
	CloseHandle(pi.hProcess);
	return (int)code;
}

// add_tailscale_peers asks the Tailscale CLI for the state and keeps every
// 100.x.y.z address. Tailscale never forwards broadcast, so this is how the
// mesh is discovered without any configuration.
static void add_tailscale_peers(void) {
	static const char *candidates[] = {
	    "tailscale status --json",
	    "\"C:\\Program Files\\Tailscale\\tailscale.exe\" status --json",
	    "\"C:\\Program Files (x86)\\Tailscale\\tailscale.exe\" status --json",
	    "Z:\\usr\\bin\\tailscale status --json",
	    NULL,
	};
	char out[MAX_PATH];
	char cmd[1400];
	int i;

	snprintf(out, sizeof(out), "%swc3lan.ts.json", g_dir);
	for (i = 0; candidates[i]; i++) {
		FILE *f;
		long size;
		char *buf;
		size_t got;
		char *p;

		DeleteFileA(out);
		snprintf(cmd, sizeof(cmd), "cmd.exe /c %s > \"%s\" 2>nul", candidates[i], out);
		if (run_wait(cmd) != 0) continue;
		f = fopen(out, "rb");
		if (!f) continue;
		fseek(f, 0, SEEK_END);
		size = ftell(f);
		fseek(f, 0, SEEK_SET);
		if (size <= 0) {
			fclose(f);
			continue;
		}
		buf = malloc((size_t)size + 1);
		if (!buf) {
			fclose(f);
			continue;
		}
		got = fread(buf, 1, (size_t)size, f);
		buf[got] = 0;
		fclose(f);
		log_line("tailscale candidate %d: %d bytes", i, (int)got);
		p = buf;
		while ((p = strstr(p, "100.")) != NULL) {
			struct in_addr ip;
			if (parse_ipv4(p, &ip)) add_peer(ip);
			p += 4;
		}
		free(buf);
		DeleteFileA(out);
		break;
	}
}

static void add_adapter_peers(void) {
	ULONG size = 16 * 1024;
	IP_ADAPTER_ADDRESSES *list = NULL;
	IP_ADAPTER_ADDRESSES *a;
	int attempt;

	for (attempt = 0; attempt < 3; attempt++) {
		list = malloc(size);
		if (!list) return;
		if (GetAdaptersAddresses(AF_INET,
		                        GAA_FLAG_SKIP_ANYCAST | GAA_FLAG_SKIP_MULTICAST |
		                            GAA_FLAG_SKIP_DNS_SERVER,
		                        NULL, list, &size) == NO_ERROR) {
			break;
		}
		free(list);
		list = NULL;
	}
	if (!list) return;

	for (a = list; a; a = a->Next) {
		IP_ADAPTER_UNICAST_ADDRESS *u;
		if (a->OperStatus != IfOperStatusUp) continue;
		for (u = a->FirstUnicastAddress; u; u = u->Next) {
			struct sockaddr_in *sin;
			struct in_addr ip, bcast;
			DWORD host, mask;
			ULONG prefix;

			if (u->Address.lpSockaddr->sa_family != AF_INET) continue;
			sin = (struct sockaddr_in *)u->Address.lpSockaddr;
			ip = sin->sin_addr;
			prefix = u->OnLinkPrefixLength;
			if (prefix == 0 || prefix >= 32) continue;
			mask = 0xFFFFFFFFu << (32 - prefix);
			host = ntohl(ip.s_addr);
			bcast.s_addr = htonl((host & mask) | ~mask);
			add_peer(bcast);
		}
	}
	free(list);
}

static void refresh_peers(int with_tailscale) {
	EnterCriticalSection(&g_lock);
	g_peer_count = 0;
	read_peers_file();
	add_adapter_peers();
	if (with_tailscale) add_tailscale_peers();
	LeaveCriticalSection(&g_lock);
}

// -------------------------------------------------------------------- hook --

static int is_lan_broadcast(const struct sockaddr *to, int tolen) {
	const struct sockaddr_in *sin;
	if (!to || tolen < (int)sizeof(struct sockaddr_in)) return 0;
	if (to->sa_family != AF_INET) return 0;
	sin = (const struct sockaddr_in *)to;
	return sin->sin_port == htons(LAN_PORT) && sin->sin_addr.s_addr == INADDR_BROADCAST;
}

static int WINAPI hook_sendto(SOCKET s, const char *buf, int len, int flags,
                              const struct sockaddr *to, int tolen) {
	const struct sockaddr_in *sin;
	int rc, i, one = 1;

	if (!real_sendto) return SOCKET_ERROR;
	if (!is_lan_broadcast(to, tolen)) {
		return real_sendto(s, buf, len, flags, to, tolen);
	}

	sin = (const struct sockaddr_in *)to;
	rc = real_sendto(s, buf, len, flags, to, tolen);

	// A subnet broadcast address needs SO_BROADCAST on the sending socket.
	setsockopt(s, SOL_SOCKET, SO_BROADCAST, (const char *)&one, sizeof(one));

	if (!g_logged_bind) {
		struct sockaddr_in local;
		int n = sizeof(local);
		if (getsockname(s, (struct sockaddr *)&local, &n) == 0) {
			struct in_addr a = local.sin_addr;
			unsigned char *b = (unsigned char *)&a;
			InterlockedExchange(&g_logged_bind, 1);
			log_line("sendto socket bound to %u.%u.%u.%u:%u", b[0], b[1],
			         b[2], b[3], ntohs(local.sin_port));
		}
	}

	EnterCriticalSection(&g_lock);
	for (i = 0; i < g_peer_count; i++) {
		struct sockaddr_in dst;
		memset(&dst, 0, sizeof(dst));
		dst.sin_family = AF_INET;
		dst.sin_port = sin->sin_port;
		dst.sin_addr = g_peers[i];
		real_sendto(s, buf, len, flags, (const struct sockaddr *)&dst,
		            sizeof(dst));
	}
	LeaveCriticalSection(&g_lock);
	return rc;
}

// patch_iat points every import of dll_name that currently resolves to from
// at to. It returns how many entries were changed.
static int patch_iat(HMODULE mod, const char *dll_name, void *from, void *to) {
	BYTE *base = (BYTE *)mod;
	IMAGE_DOS_HEADER *dos;
	IMAGE_NT_HEADERS *nt;
	IMAGE_DATA_DIRECTORY dir;
	IMAGE_IMPORT_DESCRIPTOR *imp;
	int patched = 0;

	if (!mod) return 0;
	dos = (IMAGE_DOS_HEADER *)base;
	if (dos->e_magic != IMAGE_DOS_SIGNATURE) return 0;
	nt = (IMAGE_NT_HEADERS *)(base + dos->e_lfanew);
	if (nt->Signature != IMAGE_NT_SIGNATURE) return 0;
	if (nt->OptionalHeader.Magic != IMAGE_NT_OPTIONAL_HDR32_MAGIC) return 0;
	dir = nt->OptionalHeader.DataDirectory[IMAGE_DIRECTORY_ENTRY_IMPORT];
	if (!dir.VirtualAddress || !dir.Size) return 0;

	imp = (IMAGE_IMPORT_DESCRIPTOR *)(base + dir.VirtualAddress);
	for (; imp->Name; imp++) {
		const char *name = (const char *)(base + imp->Name);
		IMAGE_THUNK_DATA *iat;
		if (lstrcmpiA(name, dll_name) != 0) continue;
		if (!imp->FirstThunk) continue;
		iat = (IMAGE_THUNK_DATA *)(base + imp->FirstThunk);
		for (; iat->u1.Function; iat++) {
			DWORD old_protect;
			if ((void *)iat->u1.Function != from) continue;
			if (!VirtualProtect(&iat->u1.Function, sizeof(void *),
			                    PAGE_READWRITE, &old_protect)) {
				continue;
			}
			iat->u1.Function = (ULONG_PTR)to;
			VirtualProtect(&iat->u1.Function, sizeof(void *), old_protect,
			               &old_protect);
			patched++;
		}
	}
	return patched;
}

// --------------------------------------------------------------- lifecycle --

static DWORD WINAPI peers_thread(LPVOID unused) {
	(void)unused;
	int tick = 0;
	for (;;) {
		refresh_peers(tick % TAILSCALE_EVERY == 0);
		tick++;
		Sleep(REFRESH_MS);
	}
	return 0;
}

static DWORD WINAPI init_thread(LPVOID unused) {
	HMODULE ws, game = NULL;
	int attempt;

	(void)unused;
	refresh_peers(0);
	log_line("shim loaded: %d peers", g_peer_count);

	ws = GetModuleHandleA("ws2_32.dll");
	if (!ws) ws = LoadLibraryA("ws2_32.dll");
	if (!ws) {
		log_line("ws2_32.dll not available");
		return 1;
	}
	real_sendto = (sendto_fn)GetProcAddress(ws, "sendto");
	if (!real_sendto) {
		log_line("sendto not found");
		return 1;
	}

	// game.dll is loaded dynamically by war3.exe.
	for (attempt = 0; attempt < 1200 && !game; attempt++) {
		game = GetModuleHandleA("game.dll");
		if (!game) Sleep(50);
	}
	if (!game) {
		log_line("game.dll never loaded");
		return 1;
	}

	// Wait until the loader resolved the import: the IAT slot still holds the
	// import lookup pointer until then.
	for (attempt = 0; attempt < 400; attempt++) {
		int n = patch_iat(game, "WS2_32.dll", (void *)real_sendto, (void *)hook_sendto);
		if (n > 0) {
			InterlockedExchange(&g_patched, 1);
			log_line("hooked %d sendto slot(s) in game.dll at %p", n, game);
			break;
		}
		Sleep(25);
	}
	if (!g_patched) log_line("could not hook game.dll (game.dll at %p)", game);

	refresh_peers(1);
	log_line("peers: %d", g_peer_count);
	peers_thread(NULL);
	return 0;
}

BOOL WINAPI DllMain(HINSTANCE inst, DWORD reason, LPVOID reserved) {
	HANDLE t;
	(void)reserved;

	if (reason != DLL_PROCESS_ATTACH) return TRUE;
	(void)inst;

	if (InterlockedCompareExchange(&g_lock_state, 1, 0) == 0) {
		InitializeCriticalSection(&g_lock);
	}
	if (InterlockedCompareExchange(&g_log_state, 1, 0) == 0) {
		InitializeCriticalSection(&g_log_lock);
	}

	// Remember the game directory so the log and the peer list live next to
	// the executable, not in the working directory.
	{
		char self[MAX_PATH];
		DWORD n = GetModuleFileNameA(inst, self, sizeof(self));
		if (n > 0) {
			char *slash = strrchr(self, '\\');
			if (slash) {
				*slash = 0;
				snprintf(g_dir, sizeof(g_dir), "%s\\", self);
			}
		}
	}

	DisableThreadLibraryCalls(inst);
	t = CreateThread(NULL, 0, init_thread, NULL, 0, NULL);
	if (t) CloseHandle(t);
	return TRUE;
}
