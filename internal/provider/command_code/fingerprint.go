package command_code

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/learningdog1/ada_proxy/internal/gateway"
)

// FingerprintSalt is the fixed salt the official CLI hashes signals with. It is
// part of the upstream contract, not a knob: the configurable
// fingerprintSalt only decides *which* fake device a credential derives.
const FingerprintSalt = "command-code:device-fingerprint:v1"

// DeviceProfile is the platform story every credential tells, so nothing
// self-contradicts: a Windows CLI does not report a Linux kernel.
type DeviceProfile struct {
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	OSRelease   string `json:"osRelease"`
	IsContainer bool   `json:"isContainer"`
	ProjectDir  string `json:"projectDir"`
}

// ProfileFor returns the device profile for a configured working directory.
func ProfileFor(projectDir string) DeviceProfile {
	if projectDir == "" {
		projectDir = DefaultProjectDir
	}
	return DeviceProfile{
		Platform:    "win32",
		Arch:        "x64",
		OSRelease:   "10.0.22631",
		IsContainer: false,
		ProjectDir:  projectDir,
	}
}

var fingerprintCPUs = []struct {
	Model string
	Cores int
}{
	{"12th Gen Intel(R) Core(TM) i7-12650H", 10},
	{"12th Gen Intel(R) Core(TM) i5-12400F", 6},
	{"12th Gen Intel(R) Core(TM) i9-12900K", 16},
	{"13th Gen Intel(R) Core(TM) i7-13700K", 16},
	{"13th Gen Intel(R) Core(TM) i5-13600K", 14},
	{"13th Gen Intel(R) Core(TM) i9-13900K", 24},
	{"Intel(R) Core(TM) Ultra 7 155H", 16},
	{"Intel(R) Core(TM) Ultra 9 285H", 16},
	{"Intel(R) Core(TM) i9-14900K", 24},
	{"Intel(R) Core(TM) i7-14700K", 20},
	{"AMD Ryzen 7 7800X3D", 8},
	{"AMD Ryzen 9 7950X", 16},
	{"AMD Ryzen 5 7600", 6},
	{"AMD Ryzen 9 7900X", 12},
	{"AMD Ryzen 7 5800X3D", 8},
}

var (
	fingerprintMems      = []int{8, 16, 24, 32, 48, 64}
	fingerprintTimezones = []string{
		"America/New_York", "America/Chicago", "America/Los_Angeles", "America/Toronto",
		"Europe/London", "Europe/Berlin", "Europe/Paris", "Europe/Moscow",
		"Asia/Shanghai", "Asia/Tokyo", "Asia/Singapore", "Asia/Seoul", "Asia/Hong_Kong",
		"Australia/Sydney", "Pacific/Auckland",
	}
	fingerprintMACCounts = []int{2, 3, 4, 5}
	fingerprintOSUsers   = []string{"dev", "user", "admin", "coder", "engineer", "work"}
	fingerprintMailHosts = []string{"gmail.com", "outlook.com", "qq.com", "163.com"}
)

// Components are the individual signals, each hashed. Empty signals are omitted
// from the wire payload but kept as "" here for the status page.
type Components struct {
	MachineIDHash    string   `json:"machineIdHash,omitempty"`
	MACHashes        []string `json:"macHashes"`
	OSUserHash       string   `json:"osUserHash,omitempty"`
	HostnameHash     string   `json:"hostnameHash,omitempty"`
	GitEmailHash     string   `json:"gitEmailHash,omitempty"`
	Platform         string   `json:"platform"`
	Arch             string   `json:"arch"`
	OSRelease        string   `json:"osRelease"`
	CPUModel         string   `json:"cpuModel"`
	CPUCount         int      `json:"cpuCount"`
	MemGiB           int      `json:"memGiB"`
	IsContainer      bool     `json:"isContainer"`
	Timezone         string   `json:"timezone"`
	Runtime          string   `json:"runtime"`
	CollectorVersion int      `json:"collectorVersion"`
}

// Fingerprint is one credential's fake device identity.
type Fingerprint struct {
	Thumbmark  string     `json:"thumbmark"`
	Components Components `json:"components"`
}

func fingerprintHash(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(FingerprintSalt + "\x00" + strings.ToLower(v)))
	return hex.EncodeToString(sum[:])
}

// GenerateFingerprint derives a stable fake device from a credential secret.
//
// The same secret always yields the same device: a device that changes between
// requests is itself the suspicious signal the upstream looks for.
func GenerateFingerprint(secret, salt, projectDir string) Fingerprint {
	p := &picker{Salt: salt, Secret: secret}
	profile := ProfileFor(projectDir)

	cpu := fingerprintCPUs[p.index("cpu", len(fingerprintCPUs), func(i int) string {
		return fmt.Sprintf("%s|%d", fingerprintCPUs[i].Model, fingerprintCPUs[i].Cores)
	})]
	memGiB := fingerprintMems[p.index("mem", len(fingerprintMems), func(i int) string {
		return fmt.Sprint(fingerprintMems[i])
	})]
	timezone := fingerprintTimezones[p.index("timezone", len(fingerprintTimezones), func(i int) string {
		return fingerprintTimezones[i]
	})]
	macCount := fingerprintMACCounts[p.index("macCount", len(fingerprintMACCounts), func(i int) string {
		return fmt.Sprint(fingerprintMACCounts[i])
	})]
	osUser := fingerprintOSUsers[p.index("osUser", len(fingerprintOSUsers), func(i int) string {
		return fingerprintOSUsers[i]
	})]
	mailHost := fingerprintMailHosts[p.index("mailDomain", len(fingerprintMailHosts), func(i int) string {
		return fingerprintMailHosts[i]
	})]

	mid := p.hex("machineId", 16)
	machineID := fmt.Sprintf("%s-%s-%s-%s-%s", mid[0:8], mid[8:12], mid[12:16], mid[16:20], mid[20:32])

	macs := make([]string, 0, macCount)
	for i := 0; i < macCount; i++ {
		b := p.digest(fmt.Sprintf("mac%d", i))[:6]
		macs = append(macs, fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5]))
	}
	sort.Strings(macs)

	hostname := "DESKTOP-" + strings.ToUpper(p.hex("hostname", 4))
	gitEmail := osUser + "." + p.hex("gitEmail", 3) + "@" + mailHost

	macHashes := make([]string, 0, len(macs))
	for _, mac := range macs {
		if h := fingerprintHash(mac); h != "" {
			macHashes = append(macHashes, h)
		}
	}

	// The thumbmark joins the machine id with the MAC list. The hostname and CPU
	// are only mixed in when there is no machine id, matching the CLI.
	seed := []string{machineID, strings.Join(macs, ",")}
	if strings.TrimSpace(machineID) == "" {
		seed = append(seed, hostname, cpu.Model)
	}
	filtered := make([]string, 0, len(seed))
	for _, s := range seed {
		if s != "" {
			filtered = append(filtered, s)
		}
	}
	thumbInput := strings.Join(filtered, "|")
	if thumbInput == "" {
		thumbInput = "unknown"
	}
	thumbSum := sha256.Sum256([]byte(FingerprintSalt + "\x00machine\x00" + thumbInput))

	return Fingerprint{
		Thumbmark: hex.EncodeToString(thumbSum[:]),
		Components: Components{
			MachineIDHash:    fingerprintHash(machineID),
			MACHashes:        macHashes,
			OSUserHash:       fingerprintHash(osUser),
			HostnameHash:     fingerprintHash(hostname),
			GitEmailHash:     fingerprintHash(gitEmail),
			Platform:         profile.Platform,
			Arch:             profile.Arch,
			OSRelease:        profile.OSRelease,
			CPUModel:         cpu.Model,
			CPUCount:         cpu.Cores,
			MemGiB:           memGiB,
			IsContainer:      profile.IsContainer,
			Timezone:         timezone,
			Runtime:          "cli",
			CollectorVersion: 1,
		},
	}
}

// picker derives every choice from one credential secret.
type picker struct {
	Salt   string
	Secret string
}

// digest is sha256(salt \0 secret \0 field), the CLI's derivation base.
func (p *picker) digest(field string) []byte {
	sum := sha256.Sum256([]byte(p.Salt + "\x00" + p.Secret + "\x00" + field))
	return sum[:]
}

// index chooses deterministically by highest digest. Unlike a modulo index,
// adding a pool entry only affects credentials whose new entry wins.
func (p *picker) index(field string, n int, labelOf func(int) string) int {
	best := 0
	var bestScore []byte
	for i := 0; i < n; i++ {
		score := p.digest(field + "\x00" + labelOf(i))
		if bestScore == nil || bytes.Compare(score, bestScore) > 0 {
			bestScore = score
			best = i
		}
	}
	return best
}

func (p *picker) hex(field string, n int) string {
	return hex.EncodeToString(p.digest(field)[:n])
}

// SlugifyProjectPath mirrors the CLI's slugify for the working directory: lower
// case, runs of non-alphanumerics collapse to one hyphen, no trailing hyphen.
func SlugifyProjectPath(p string) string {
	var b strings.Builder
	lastHyphen := true // suppresses a leading hyphen
	for _, r := range strings.ToLower(p) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "root"
	}
	return out
}

// asIdentity projects the vendor shape onto the adapter-neutral one.
func (f Fingerprint) asIdentity() gateway.Identity {
	components := map[string]any{
		"machineIdHash":    f.Components.MachineIDHash,
		"macHashes":        f.Components.MACHashes,
		"osUserHash":       f.Components.OSUserHash,
		"hostnameHash":     f.Components.HostnameHash,
		"gitEmailHash":     f.Components.GitEmailHash,
		"platform":         f.Components.Platform,
		"arch":             f.Components.Arch,
		"osRelease":        f.Components.OSRelease,
		"cpuModel":         f.Components.CPUModel,
		"cpuCount":         f.Components.CPUCount,
		"memGiB":           f.Components.MemGiB,
		"isContainer":      f.Components.IsContainer,
		"timezone":         f.Components.Timezone,
		"runtime":          f.Components.Runtime,
		"collectorVersion": f.Components.CollectorVersion,
	}
	return gateway.Identity{Thumbmark: f.Thumbmark, Components: components}
}
