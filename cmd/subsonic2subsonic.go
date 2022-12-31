package main

// Notes:
// -   Normalizes paths to lower case because iTunes/Windows doesn't update if the underlying file changes.
// -   Navidrome requires going into the Player settings and configuring "Report Real Path"

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/delucks/go-subsonic"
	pb "github.com/schollz/progressbar/v3"
)

var (
	dryRun          = flag.Bool("dry_run", true, "don't modify the library")
	skipCount       = flag.Int("skip_count", 10, "a limit on the number of tracks that would be skipped before refusing to process")
	subsonicSrcUrl  = flag.String("subsonic_src", "", "url of the Subsonic instance to read")
	subsonicDstUrl  = flag.String("subsonic_dst", "", "url of the Subsonic instance to write to")
	subsonicSrcRoot = flag.String("subsonic_src_root", "", "the music library prefix on the read instance")
	subsonicDstRoot = flag.String("subsonic_dst_root", "/music/", "the music library prefix on the write instance")
)

type subsonicInfo struct {
	id     string
	rating int
}

type track struct {
	src subsonicInfo
	dst subsonicInfo
}

// PbWithOptions applies options to a progressbar. I like the default, but the
// saucer is something that doesn't render well in my terminal.
func PbWithOptions(p *pb.ProgressBar) *pb.ProgressBar {
	pb.OptionSetTheme(pb.Theme{Saucer: "=", SaucerPadding: " ", BarStart: "[", BarEnd: "]"})(p)
	return p
}

func main() {
	flag.Parse()
	subsonicSrcUser, subsonicSrcPass := os.Getenv("SUBSONIC_SRC_USER"), os.Getenv("SUBSONIC_SRC_PASS")
	subsonicDstUser, subsonicDstPass := os.Getenv("SUBSONIC_USER"), os.Getenv("SUBSONIC_PASS")

	if (subsonicSrcUser != "" || *subsonicSrcUrl != "") && subsonicSrcPass == "" {
		log.Fatal("If reading from Subsonic, you must set the SUBSONIC_SRC_USER and SUBSONIC_SRC_PASS environment variables.")
	}
	if (subsonicDstUser != "" || *subsonicDstUrl != "") && subsonicDstPass == "" {
		log.Fatal("If writing to Subsonic, you must set the SUBSONIC_USER and SUBSONIC_PASS environment variables.")
	}

	// Map by file path.
	tracks := make(map[string]*track)
	skip := 0

	var srcC *subsonic.Client
	if *subsonicSrcUrl != "" {
		srcC = &subsonic.Client{
			Client:         &http.Client{},
			BaseUrl:        *subsonicSrcUrl,
			User:           subsonicSrcUser,
			PasswordAuth:   true,
			ClientName:     "subsonic2subsonic",
			RequireDotView: true,
		}
		err := srcC.Authenticate(subsonicSrcPass)
		if err != nil {
			log.Fatalf("Failed to create Subsonic client: %s", err)
		}

		offset := 0
		trackCount := 0
		var bar *pb.ProgressBar
		for {
			songs, err := srcC.Search3(`""`, map[string]string{
				"songCount":   "400",
				"songOffset":  strconv.Itoa(offset),
				"artistCount": "0",
				"albumCount":  "0",
			})
			if err != nil {
				log.Fatalf("Failed fetching Subsonic songs: %s", err)
			}

			if bar == nil {
				bar = PbWithOptions(pb.Default(-1, "fetching src"))
			}
			for _, s := range songs.Song {
				if !strings.HasPrefix(s.Path, *subsonicSrcRoot) {
					log.Printf("Warning: Unusual Subsonic location: %s `%s`", s.Title, s.Path)
					skip++
					if *skipCount > 0 && skip > *skipCount {
						log.Fatalf("Too many skipped tracks. Failing out...")
					}
					continue
				}

				trackCount++
				loc := strings.ToLower(strings.TrimPrefix(s.Path, *subsonicSrcRoot))
				t, ok := tracks[loc]
				if !ok {
					t = &track{}
					tracks[loc] = t
				}
				t.src.id = s.ID
				t.src.rating = s.UserRating
			}

			if len(songs.Song) == 0 {
				bar.Finish()
				break
			}

			offset += len(songs.Song)
			bar.Set(offset)
		}

		log.Printf("Subsonic Src: track count %d\n", trackCount)
	}

	var dstC *subsonic.Client
	if *subsonicDstUrl != "" {
		dstC = &subsonic.Client{
			Client:     &http.Client{},
			BaseUrl:    *subsonicDstUrl,
			User:       subsonicDstUser,
			ClientName: "subsonic2subsonic",
		}
		err := dstC.Authenticate(subsonicDstPass)
		if err != nil {
			log.Fatalf("Failed to create Subsonic client: %s", err)
		}

		offset := 0
		trackCount := 0
		var bar *pb.ProgressBar
		for {
			songs, err := dstC.Search3(`""`, map[string]string{
				"songCount":   "400",
				"songOffset":  strconv.Itoa(offset),
				"artistCount": "0",
				"albumCount":  "0",
			})
			if err != nil {
				log.Fatalf("Failed fetching Subsonic songs: %s", err)
			}

			if bar == nil {
				bar = PbWithOptions(pb.Default(-1, "fetching dst"))
			}
			for _, s := range songs.Song {
				if !strings.HasPrefix(s.Path, *subsonicDstRoot) {
					log.Printf("Warning: Unusual Subsonic location: %s `%s`", s.Title, s.Path)
					skip++
					if *skipCount > 0 && skip > *skipCount {
						log.Fatalf("Too many skipped tracks. Failing out...")
					}
					continue
				}

				trackCount++
				loc := strings.ToLower(strings.TrimPrefix(s.Path, *subsonicDstRoot))
				t, ok := tracks[loc]
				if !ok {
					t = &track{}
					tracks[loc] = t
				}
				t.dst.id = s.ID
				t.dst.rating = s.UserRating
			}

			if len(songs.Song) == 0 {
				bar.Finish()
				break
			}

			offset += len(songs.Song)
			bar.Set(offset)
		}

		log.Printf("Subsonic Dst: track count %d\n", trackCount)
	}

	fmt.Println("== Missing Tracks ==")
	for k, v := range tracks {
		if v.src.id != "" && v.dst.id != "" {
			continue
		}

		fmt.Printf("%s\n\tsrc(%s)\tdst(%s)\n", k, v.src.id, v.dst.id)
	}
	fmt.Println("")

	fmt.Println("== Mismatched Ratings ==")
	var mismatchCount int64 = 0
	for k, v := range tracks {
		if (v.src.id == "" || v.dst.id == "") || v.src.rating == v.dst.rating {
			continue
		}

		fmt.Printf("%s\n\tsrc(%d)\tdst(%d)\n", k, v.src.rating, v.dst.rating)
		mismatchCount++
	}
	fmt.Println("")

	fmt.Printf("== Copy %d Ratings To Subsonic ==\n", mismatchCount)
	if dstC != nil && !*dryRun {
		// Pause to give the user a chance to quit.
		time.Sleep(400 * time.Millisecond)

		bar := PbWithOptions(pb.Default(mismatchCount, "set rating"))
		for k, v := range tracks {
			if (v.src.id == "" || v.dst.id == "") || v.src.rating == v.dst.rating {
				continue
			}

			err := dstC.SetRating(v.dst.id, v.src.rating)
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
}
