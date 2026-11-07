package command_code

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// fixedSecret is a fake credential. Golden files only ever contain fakes.
const fixedSecret = "user_0123456789abcdef0123456789abcdef"

func TestFingerprintIsDeterministic(t *testing.T) {
	a := GenerateFingerprint(fixedSecret, "", DefaultProjectDir)
	b := GenerateFingerprint(fixedSecret, "", DefaultProjectDir)
	if a.Thumbmark != b.Thumbmark {
		t.Fatalf("thumbmark changed between calls: %s vs %s", a.Thumbmark, b.Thumbmark)
	}
	if a.Components.CPUModel != b.Components.CPUModel {
		t.Fatal("cpu model changed between calls")
	}
	if len(a.Components.MACHashes) != len(b.Components.MACHashes) {
		t.Fatal("mac count changed between calls")
	}
}

func TestFingerprintDiffersPerSecret(t *testing.T) {
	a := GenerateFingerprint(fixedSecret, "", DefaultProjectDir)
	b := GenerateFingerprint("user_fedcba9876543210fedcba9876543210", "", DefaultProjectDir)
	if a.Thumbmark == b.Thumbmark {
		t.Fatal("two different credentials produced the same device")
	}
}

func TestFingerprintSaltSelectsAnotherDevice(t *testing.T) {
	plain := GenerateFingerprint(fixedSecret, "", DefaultProjectDir)
	salted := GenerateFingerprint(fixedSecret, "custom-salt", DefaultProjectDir)
	if plain.Thumbmark == salted.Thumbmark {
		t.Fatal("the configurable salt did not change the derived device")
	}
	// Both must still describe the same platform: only the device changes, never
	// the story.
	if plain.Components.Platform != salted.Components.Platform {
		t.Fatal("platform profile changed with the salt")
	}
}

func TestFingerprintHashMatchesTheCLIFormula(t *testing.T) {
	const value = "DEV-SIGNAL"
	sum := sha256.Sum256([]byte(FingerprintSalt + "\x00" + strings.ToLower(value)))
	want := hex.EncodeToString(sum[:])
	if got := fingerprintHash(value); got != want {
		t.Fatalf("fingerprintHash = %s, want %s", got, want)
	}
	if got := fingerprintHash("   "); got != "" {
		t.Fatalf("blank signal hashed to %q, want empty", got)
	}
}

func TestFingerprintComponentsAreConsistent(t *testing.T) {
	fp := GenerateFingerprint(fixedSecret, "", DefaultProjectDir)
	c := fp.Components

	if c.Platform != "win32" || c.Arch != "x64" {
		t.Fatalf("platform = %s/%s", c.Platform, c.Arch)
	}
	if c.Runtime != "cli" || c.CollectorVersion != 1 {
		t.Fatalf("runtime/collector = %s/%d", c.Runtime, c.CollectorVersion)
	}
	if c.CPUModel == "" || c.CPUCount <= 0 {
		t.Fatalf("cpu = %q/%d", c.CPUModel, c.CPUCount)
	}
	if c.MemGiB < 8 || c.MemGiB > 64 {
		t.Fatalf("mem = %d GiB, outside the advertised pool", c.MemGiB)
	}
	if len(c.MACHashes) < 2 || len(c.MACHashes) > 5 {
		t.Fatalf("mac count = %d, outside 2..5", len(c.MACHashes))
	}
	for _, h := range c.MACHashes {
		if len(h) != 64 {
			t.Fatalf("mac hash %q is not a sha256 hex digest", h)
		}
	}
	for name, h := range map[string]string{
		"machineIdHash": c.MachineIDHash,
		"osUserHash":    c.OSUserHash,
		"hostnameHash":  c.HostnameHash,
		"gitEmailHash":  c.GitEmailHash,
	} {
		if h == "" {
			t.Fatalf("%s is empty", name)
		}
		if len(h) != 64 {
			t.Fatalf("%s = %q, not a sha256 hex digest", name, h)
		}
	}
}

func TestFingerprintGolden(t *testing.T) {
	for name, salt := range map[string]string{
		"fingerprint.json":        "",
		"fingerprint-salted.json": "test-salt-123",
	} {
		fp := GenerateFingerprint(fixedSecret, salt, DefaultProjectDir)
		raw, err := jsonMarshalIndent(fp)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		checkGolden(t, name, raw)
	}
}

func TestSlugifyProjectPath(t *testing.T) {
	cases := map[string]string{
		`C:\Users\dev\projects\app`: "c-users-dev-projects-app",
		`/home/dev/My App`:          "home-dev-my-app",
		"":                          "root",
		"///":                       "root",
		"project":                   "project",
		"  spaced  out  ":           "spaced-out",
	}
	for in, want := range cases {
		if got := SlugifyProjectPath(in); got != want {
			t.Errorf("SlugifyProjectPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProfileFor(t *testing.T) {
	p := ProfileFor("")
	if p.ProjectDir != DefaultProjectDir {
		t.Fatalf("default project dir = %q", p.ProjectDir)
	}
	custom := ProfileFor(`D:\work\thing`)
	if custom.ProjectDir != `D:\work\thing` {
		t.Fatalf("custom project dir = %q", custom.ProjectDir)
	}
	if custom.Platform != p.Platform || custom.Arch != p.Arch {
		t.Fatal("platform profile differs for a custom directory")
	}
}
