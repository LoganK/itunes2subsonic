package itunes2subsonic

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	pb "github.com/schollz/progressbar/v3"
)

// PbWithOptions applies options to a progressbar. I like the default, but the
// saucer is something that doesn't render well in my terminal.
func PbWithOptions(p *pb.ProgressBar) *pb.ProgressBar {
	pb.OptionSetTheme(pb.Theme{Saucer: "=", SaucerPadding: " ", BarStart: "[", BarEnd: "]"})(p)
	return p
}

// longestLibraryPrefix returns the longest prefix that would match the given 2
// paths assuming they were the same file. If nothing matches, returns the input
// strings.
func longestLibraryPrefix(a, b string) (string, string) {
	var aDir, bDir string
	for aDir, bDir = a, b; aDir != "" && bDir != ""; {
		ad, af := filepath.Split(strings.TrimSuffix(aDir, string(os.PathSeparator)))
		bd, bf := filepath.Split(strings.TrimSuffix(bDir, string(os.PathSeparator)))

		if af != bf {
			break
		}

		aDir, bDir = ad, bd
	}

	return aDir, bDir
}

// LibraryPrefix finds the most likely library root for the given src and dst
// assuming that both libraries contain mostly the same music.
//
// Note: All paths normalized to lower case.
func LibraryPrefix(src, dst []SongInfo) (string, string) {
	// Pick an obviously wrong suffix as an empty string indicates matching libraries.
	wrong := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	srcPrefix, dstPrefix := wrong, wrong

	// Sample at random as the beginning or end of the libraries may not line up. Pick a limit because this is an expensive scan.
	for i := 0; i < 500; i++ {
		// User lower case for systems like Windows where case can change without triggering a library update.
		srcP := strings.ToLower(src[rand.Intn(len(src))].Path())
		for _, d := range dst {
			dstP := strings.ToLower(d.Path())
			if srcP == "" || dstP == "" {
				continue
			}

			sp, dp := longestLibraryPrefix(srcP, dstP)
			if len(sp) < len(srcPrefix) {
				srcPrefix = sp
			}
			if len(dp) < len(dstPrefix) {
				dstPrefix = dp
			}
		}
	}

	return srcPrefix, dstPrefix
}
