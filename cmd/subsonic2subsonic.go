package main

// Notes:
// -   Normalizes paths to lower case because iTunes/Windows doesn't update if the underlying file changes.
// -   Navidrome requires going into the Player settings and configuring "Report Real Path"

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/delucks/go-subsonic"
	i2s "github.com/logank/itunes2subsonic"
	pb "github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"
)

var (
	dryRun          = flag.Bool("dry_run", true, "don't modify the library")
	skipCount       = flag.Int("skip_count", 10, "a limit on the number of tracks that would be skipped before refusing to process")
	subsonicSrcUrl  = flag.String("subsonic_src", "", "url of the Subsonic instance to read")
	subsonicDstUrl  = flag.String("subsonic_dst", "", "url of the Subsonic instance to write to")
	subsonicSrcRoot = flag.String("subsonic_src_root", "", "(optional) the music library prefix on the read instance")
	subsonicDstRoot = flag.String("subsonic_dst_root", "", "(optional) the music library prefix on the write instance")
)

type subsonicInfo struct {
	id     string
	path   string
	rating int
}

func (s subsonicInfo) Id() string          { return s.id }
func (s subsonicInfo) Path() string        { return s.path }
func (s subsonicInfo) FiveStarRating() int { return s.rating }

type songPair struct {
	src i2s.SongInfo
	dst i2s.SongInfo
}

func fetchSubsonicSongs(c *subsonic.Client, bar *pb.ProgressBar) ([]i2s.SongInfo, error) {
	var tracks []i2s.SongInfo

	offset := 0
	for {
		songs, err := c.Search3(`""`, map[string]string{
			"songCount":   "400",
			"songOffset":  strconv.Itoa(offset),
			"artistCount": "0",
			"albumCount":  "0",
		})
		if err != nil {
			log.Fatalf("Failed fetching Subsonic songs: %s", err)
		}

		for _, s := range songs.Song {
			tracks = append(tracks, subsonicInfo{
				id:     s.ID,
				path:   s.Path,
				rating: s.UserRating,
			})
		}

		if len(songs.Song) == 0 {
			break
		}

		offset += len(songs.Song)
		bar.Add(len(songs.Song))
	}

	return tracks, nil

}

func main() {
	ctx := context.Background()

	flag.Parse()
	subsonicSrcUser, subsonicSrcPass := os.Getenv("SUBSONIC_SRC_USER"), os.Getenv("SUBSONIC_SRC_PASS")
	subsonicDstUser, subsonicDstPass := os.Getenv("SUBSONIC_USER"), os.Getenv("SUBSONIC_PASS")

	if *subsonicSrcUrl == "" || *subsonicDstUrl == "" {
		log.Fatal("You must provide both --subsonic_src and --subsonic_dst")
	}

	if subsonicSrcUser == "" || subsonicSrcPass == "" {
		log.Fatal("You must set the SUBSONIC_SRC_USER and SUBSONIC_SRC_PASS environment variables.")
	}
	if subsonicDstUser == "" || subsonicDstPass == "" {
		log.Fatal("You must set the SUBSONIC_USER and SUBSONIC_PASS environment variables.")
	}

	srcC := &subsonic.Client{
		Client:         &http.Client{},
		BaseUrl:        *subsonicSrcUrl,
		User:           subsonicSrcUser,
		PasswordAuth:   true,
		ClientName:     "subsonic2subsonic",
		RequireDotView: true,
	}
	if err := srcC.Authenticate(subsonicSrcPass); err != nil {
		log.Fatalf("Failed to create Subsonic client: %s", err)
	}

	dstC := &subsonic.Client{
		Client:         &http.Client{},
		BaseUrl:        *subsonicDstUrl,
		User:           subsonicDstUser,
		ClientName:     "subsonic2subsonic",
		RequireDotView: true,
	}
	if err := dstC.Authenticate(subsonicDstPass); err != nil {
		log.Fatalf("Failed to create Subsonic client: %s", err)
	}

	var srcSongs, dstSongs []i2s.SongInfo
	g, _ := errgroup.WithContext(ctx)
	fetchBar := i2s.PbWithOptions(pb.Default(-1, "fetching subsonic data"))
	g.Go(func() error {
		var err error
		srcSongs, err = fetchSubsonicSongs(srcC, fetchBar)
		return err
	})
	g.Go(func() error {
		var err error
		dstSongs, err = fetchSubsonicSongs(dstC, fetchBar)
		return err
	})
	if err := g.Wait(); err != nil {
		log.Fatalf("Failed while fetching Subsonic info: %s", err)
	}
	fetchBar.Finish()

	log.Printf("Subsonic Src track count %d, Dst track count %d\n", len(srcSongs), len(dstSongs))

	if *subsonicSrcRoot == "" && *subsonicDstRoot == "" {
		*subsonicSrcRoot, *subsonicDstRoot = i2s.LibraryPrefix(srcSongs, dstSongs)
	}
	fmt.Printf("Music library root: src='%s' dst='%s'\n", *subsonicSrcRoot, *subsonicDstRoot)

	byPath := make(map[string]*songPair)
	for _, s := range srcSongs {
		p := strings.ToLower(strings.TrimPrefix(s.Path(), *subsonicSrcRoot))
		t, ok := byPath[p]
		if !ok {
			t = &songPair{dst: subsonicInfo{}}
			byPath[p] = t
		}
		t.src = s
	}
	for _, s := range dstSongs {
		p := strings.ToLower(strings.TrimPrefix(s.Path(), *subsonicDstRoot))
		t, ok := byPath[p]
		if !ok {
			t = &songPair{src: subsonicInfo{}}
			byPath[p] = t
		}
		t.dst = s
	}

	fmt.Println("== Missing Tracks ==")
	missingCount := 0
	for k, v := range byPath {
		if v.src.Id() != "" && v.dst.Id() != "" {
			continue
		}

		missingCount++
		fmt.Printf("%s\n\tmissing src(%s)\tdst(%s)\n", k, v.src.Id(), v.dst.Id())
	}
	fmt.Println("")
	fmt.Printf("== Missing Track Count %d / (%d + %d) ==\n", missingCount, len(srcSongs), len(dstSongs))

	if 100*missingCount/(len(srcSongs)+len(dstSongs)) > 90 {
		fmt.Printf(`Warning: Missing count is significant. Tips:
* Verify that the libraries are configured for the same directory
* Set --subsonic_src_root and --subsonic_dst_root to the correct values
* In Navidrome Player Settings, configure "Report Real Path"\n`)
	}

	fmt.Println("== Mismatched Ratings ==")
	var mismatchCount int64 = 0
	for k, v := range byPath {
		if v.src.Id() == "" || v.dst.Id() == "" || v.src.FiveStarRating() == v.dst.FiveStarRating() {
			continue
		}

		fmt.Printf("%s\n\trating src(%d)\tdst(%d)\n", k, v.src.FiveStarRating(), v.dst.FiveStarRating())
		mismatchCount++
	}
	fmt.Println("")

	fmt.Printf("== Copy %d Ratings To Subsonic ==\n", mismatchCount)
	if *dryRun {
		fmt.Printf("Set --dry_run=false to modify %s", *subsonicDstUrl)
	} else {
		// Pause to give the user a chance to quit.
		time.Sleep(400 * time.Millisecond)

		skip := 0
		bar := i2s.PbWithOptions(pb.Default(mismatchCount, "set rating"))
		for k, v := range byPath {
			if v.src.Id() == "" || v.dst.Id() == "" || v.src.FiveStarRating() == v.dst.FiveStarRating() {
				continue
			}

			err := dstC.SetRating(v.dst.Id(), v.src.FiveStarRating())
			bar.Add(1)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error setting rating for '%s': %s\n", k, err)
				skip++
				if *skipCount > 0 && skip > *skipCount {
					log.Fatalf("Too many skipped tracks. Failing out...")
				}
			}
		}
		bar.Finish()
	}
	fmt.Println("")
}
