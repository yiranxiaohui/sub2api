package persona

// DefaultPool returns a curated pool that approximates the real-world
// distribution of Claude Code installations. Values are conservative and
// hand-checked; production deployments are expected to refine them as the
// CLI evolves and new beta flags ship.
//
// The returned Pool has not been Validated yet — callers must call
// Pool.Validate() before passing it to SelectPersona. This lets callers
// patch fields (e.g. inject runtime-only TLSProfileIDs) before validation.
//
// Weights are integers because viper/yaml parses them as int more reliably
// than float. Normalization happens during selection.
func DefaultPool() *Pool {
	return &Pool{
		CLIVersions: defaultCLIVersions(),
		OSCombos: []OSCombo{
			defaultMacOSArm64Combo(),
			defaultMacOSX64Combo(),
			defaultLinuxX64Combo(),
			defaultLinuxArm64Combo(),
			defaultWindowsX64Combo(),
		},
		BetaVariants:            defaultBetaVariants(),
		LocaleVariants:          defaultLocaleVariants(),
		TLSProfileIDs:           nil, // admin-supplied at runtime
		CacheBreakpointVariants: defaultCacheBreakpointVariants(),
	}
}

// defaultCLIVersions distributes accounts across recent CLI releases with a
// long tail. A pool that puts 100% of accounts on the latest version is
// statistically suspicious — real fleets show a clear bell curve around the
// last few minor releases plus a stale long tail.
func defaultCLIVersions() []CLIVersionEntry {
	return []CLIVersionEntry{
		{Version: "2.1.92", Weight: 40}, // latest as of pool definition
		{Version: "2.1.87", Weight: 25},
		{Version: "2.1.81", Weight: 20},
		{Version: "2.1.75", Weight: 10},
		{Version: "2.1.70", Weight: 5}, // stale tail
	}
}

// defaultMacOSArm64Combo is the largest cluster in the wild — Apple Silicon
// developer laptops. Apple ships new macOS major every year so kernel pool
// covers macOS 14 (Darwin 23) and 15 (Darwin 24).
func defaultMacOSArm64Combo() OSCombo {
	return OSCombo{
		ID:     "macos_arm64",
		Weight: 35,
		OS:     "MacOS",
		Arch:   "arm64",
		KernelPool: []string{
			"23.4.0", // macOS 14.4 Sonoma
			"23.6.0", // macOS 14.6 Sonoma
			"24.1.0", // macOS 15.1 Sequoia
			"24.2.0", // macOS 15.2 Sequoia
		},
		CPUPool: []string{
			"Apple M1",
			"Apple M1 Pro",
			"Apple M1 Max",
			"Apple M2",
			"Apple M2 Pro",
			"Apple M3",
			"Apple M3 Pro",
			"Apple M3 Max",
			"Apple M4",
		},
		NodeVersionPool: []string{
			"v20.18.1",
			"v20.19.0",
			"v22.11.0",
			"v22.12.0",
		},
		HostnamePrefixPool: []string{"mbp", "mba", "mac-mini", "imac", "studio"},
	}
}

// defaultMacOSX64Combo covers the dwindling Intel Mac population — still
// non-zero but small. Including it makes the fleet look natural.
func defaultMacOSX64Combo() OSCombo {
	return OSCombo{
		ID:     "macos_x64",
		Weight: 8,
		OS:     "MacOS",
		Arch:   "x64",
		KernelPool: []string{
			"22.6.0", // macOS 13 Ventura (last fully x64-supported)
			"23.6.0", // macOS 14 Sonoma (also runs on Intel)
		},
		CPUPool: []string{
			"Intel Core i5-1038NG7",
			"Intel Core i7-1068NG7",
			"Intel Core i9-9980HK",
			"Intel Xeon W-3245M",
		},
		NodeVersionPool: []string{
			"v20.18.1",
			"v22.11.0",
		},
		HostnamePrefixPool: []string{"mbp", "imac", "mac-pro"},
	}
}

// defaultLinuxX64Combo is the second-largest cluster — Linux developers on
// x86_64. Ubuntu / Debian / Fedora cover most.
func defaultLinuxX64Combo() OSCombo {
	return OSCombo{
		ID:     "linux_x64",
		Weight: 25,
		OS:     "Linux",
		Arch:   "x64",
		KernelPool: []string{
			"6.5.0-15-generic",  // Ubuntu 23.10
			"6.8.0-49-generic",  // Ubuntu 24.04
			"6.11.0-13-generic", // Ubuntu 24.10
			"6.1.0-21-amd64",    // Debian 12
		},
		CPUPool: []string{
			"Intel Core i7-12700H",
			"Intel Core i7-13700H",
			"Intel Core i9-13900H",
			"AMD Ryzen 7 7840U",
			"AMD Ryzen 9 7945HX",
			"Intel Xeon Gold 6248R",
		},
		NodeVersionPool: []string{
			"v20.18.1",
			"v22.11.0",
			"v22.12.0",
		},
		HostnamePrefixPool: []string{"ubuntu", "debian", "fedora", "ws", "dev"},
	}
}

// defaultLinuxArm64Combo covers Apple Silicon Linux VMs (Asahi, UTM) and
// AWS Graviton / Ampere developer instances. Smaller cluster.
func defaultLinuxArm64Combo() OSCombo {
	return OSCombo{
		ID:     "linux_arm64",
		Weight: 5,
		OS:     "Linux",
		Arch:   "arm64",
		KernelPool: []string{
			"6.5.0-1023-aws", // AWS Graviton
			"6.8.0-49-generic",
			"6.6.0-asahi",
		},
		CPUPool: []string{
			"AWS Graviton3", // not Apple — Ampere Altra family
			"Ampere Altra Q80-30",
			"Cortex-A78 r0p1",
		},
		NodeVersionPool: []string{
			"v20.18.1",
			"v22.11.0",
		},
		HostnamePrefixPool: []string{"ip-", "graviton", "arm64-dev"},
	}
}

// defaultWindowsX64Combo covers the Windows developer population. Generally
// smaller than macOS/Linux for Claude Code in the wild but non-trivial.
func defaultWindowsX64Combo() OSCombo {
	return OSCombo{
		ID:     "windows_x64",
		Weight: 17,
		OS:     "Windows",
		Arch:   "x64",
		KernelPool: []string{
			"10.0.22631", // Windows 11 23H2
			"10.0.26100", // Windows 11 24H2
			"10.0.19045", // Windows 10 22H2
		},
		CPUPool: []string{
			"Intel Core i7-12700H",
			"Intel Core i7-13700H",
			"Intel Core i9-13900H",
			"AMD Ryzen 7 7840HS",
			"AMD Ryzen 9 7940HS",
		},
		NodeVersionPool: []string{
			"v20.18.1",
			"v22.11.0",
		},
		HostnamePrefixPool: []string{"DESKTOP", "LAPTOP", "WIN"},
	}
}

// defaultBetaVariants spreads accounts across plausible anthropic-beta flag
// combinations. Real Claude Code installs send slightly different flag sets
// depending on CLI version, IDE integration, and enrollment in beta channels.
//
// Flag IDs are kept as literal strings here so this package has no upward
// dependency on internal/pkg/claude. Higher layers map BetaVariantID →
// literal flag list (or directly use the Flags here, which already matches
// claude.Beta* constants).
func defaultBetaVariants() []BetaVariant {
	return []BetaVariant{
		{
			ID:     "minimal",
			Weight: 15,
			Flags: []string{
				"claude-code-20250219",
				"oauth-2025-04-20",
			},
		},
		{
			ID:     "thinking",
			Weight: 30,
			Flags: []string{
				"claude-code-20250219",
				"oauth-2025-04-20",
				"interleaved-thinking-2025-05-14",
			},
		},
		{
			ID:     "core",
			Weight: 25,
			Flags: []string{
				"claude-code-20250219",
				"oauth-2025-04-20",
				"interleaved-thinking-2025-05-14",
				"effort-2025-11-24",
				"context-management-2025-06-27",
			},
		},
		{
			ID:     "full",
			Weight: 30,
			Flags: []string{
				"claude-code-20250219",
				"oauth-2025-04-20",
				"interleaved-thinking-2025-05-14",
				"prompt-caching-scope-2026-01-05",
				"effort-2025-11-24",
				"context-management-2025-06-27",
				"extended-cache-ttl-2025-04-11",
			},
		},
	}
}

// defaultLocaleVariants. NOTE: locale should ultimately be chosen to match
// the proxy IP geography. SelectPersona uses pool order; callers can post-
// filter by proxy region if known.
func defaultLocaleVariants() []LocaleVariant {
	return []LocaleVariant{
		{Lang: "en-US", TZ: "America/Los_Angeles", Weight: 25},
		{Lang: "en-US", TZ: "America/New_York", Weight: 20},
		{Lang: "en-US", TZ: "America/Chicago", Weight: 10},
		{Lang: "en-GB", TZ: "Europe/London", Weight: 10},
		{Lang: "de-DE", TZ: "Europe/Berlin", Weight: 5},
		{Lang: "fr-FR", TZ: "Europe/Paris", Weight: 4},
		{Lang: "zh-CN", TZ: "Asia/Shanghai", Weight: 12},
		{Lang: "ja-JP", TZ: "Asia/Tokyo", Weight: 8},
		{Lang: "ko-KR", TZ: "Asia/Seoul", Weight: 3},
		{Lang: "pt-BR", TZ: "America/Sao_Paulo", Weight: 3},
	}
}

// defaultCacheBreakpointVariants exposes which prompt-cache strategy each
// account uses. Higher layers map ID → cache breakpoint policy.
//
// "default" matches the project's pre-persona behaviour (two breakpoints,
// 5m TTL); other variants exist so accounts don't all share an identical
// prompt-cache shape.
func defaultCacheBreakpointVariants() []CacheBreakpointVariant {
	return []CacheBreakpointVariant{
		{ID: "default", Weight: 60},  // current behaviour
		{ID: "single", Weight: 15},   // only last message
		{ID: "earlier", Weight: 15},  // 4-back + last
		{ID: "extended", Weight: 10}, // 1h TTL
	}
}
