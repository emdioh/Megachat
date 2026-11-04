package media

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mediaParityNames are the awkward file names compared against Python's
// filename_utils.sanitize_filename.
var mediaParityNames = []string{
	"plain.png", "ＦＵＬＬＷＩＤＴＨ.jpg", "ﬁle ﬂow ﬃ.pdf", "①②③.txt", "éclaiŕ.png",
	"à́b.mp4", "ｶﾞｷﾞ.png", "℡ ™ ㎏.zip", "..\\..\\evil/../name.png", "  .hidden. ", ".bashrc",
	"report.final.docx", "tab\tand\nnewline.png", "emoji 😀.png", "ǆ ǅ Ǆ.png", "x²³.png", "‥…⁇.png",
	"日本語ファイル.pdf", "a" + strings.Repeat("ｂ", 300) + ".png", strings.Repeat("x", 254) + ".pdf",
	strings.Repeat("é", 260), strings.Repeat("y", 260) + ".", "", "trailing dots...", "a b   c.png",
}

func mediaPythonSanitize(t *testing.T, names []string) []string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	root, _ := filepath.Abs("../../..")
	script := "import sys,json\nsys.path.insert(0,sys.argv[1])\nfrom filename_utils import sanitize_filename\n" +
		"print(json.dumps([sanitize_filename(n) for n in json.load(sys.stdin)]))\n"
	cmd := exec.Command(py, "-c", script, root)
	in, _ := json.Marshal(names)
	cmd.Stdin = strings.NewReader(string(in))
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("python sanitiser unavailable: %v", err)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMediaSanitizeFilenameParityWithPython(t *testing.T) {
	want := mediaPythonSanitize(t, mediaParityNames)
	for i, n := range mediaParityNames {
		if got := SanitizeFilename(n); got != want[i] {
			t.Errorf("SanitizeFilename(%q) = %q, python %q", n, got, want[i])
		}
	}
}

func TestMediaSanitizeFilenameNFKC(t *testing.T) {
	if got := SanitizeFilename("ＦＵＬＬ ﬁle.png"); got != "FULL file.png" {
		t.Errorf("got %q", got)
	}
}
