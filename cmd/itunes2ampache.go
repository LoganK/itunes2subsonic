package main

// Notes:
// -   Normalizes paths to lower case because iTunes/Windows doesn't update if the underlying file changes.

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/logank/ampache"
	"github.com/logank/itunes2ampache/internal/itunes"
)

var (
	dryRun      = true // --dry_run
	itunesXml   = flag.String("itunes_xml", "", "path to the itunes XML to import")
	skipCount   = flag.Int("skip_count", 10, "a limit on the number of tracks that would be skipped before refusing to process")
	ampacheUrl  = flag.String("ampache", "", "url of the Ampache instance")
	itunesRoot  = `file://localhost/M:/Music`
	ampacheRoot = `/media`
)

func init() {
	flag.BoolVar(&dryRun, "dry_run", true, "don't modify the library")
	flag.BoolVar(&dryRun, "n", true, "don't modify the library")
}

type itunesRating int

func (r itunesRating) AsAmpacheRating() ampacheRating {
	return ampacheRating(r / 20)
}

func (r itunesRating) Equal(ar ampacheRating) bool {
	return r.AsAmpacheRating() == ar
}

type ampacheRating int

type track struct {
	itunesId     int
	itunesRating itunesRating

	ampacheId     int
	ampacheRating ampacheRating
}

func main() {
	flag.Parse()
	ampacheUser, ampachePass := os.Getenv("AMPACHE_USER"), os.Getenv("AMPACHE_PASS")

	if *itunesXml == "" {
		log.Fatal("You must provide --itunes_xml")
	}
	if (ampacheUser != "" || *ampacheUrl != "") && ampachePass == "" {
		log.Fatal("If connecting to Ampache, you must set the AMPACHE_USER and AMPACHE_PASS environment variables.")
	}

	// Map by file path.
	tracks := make(map[string]*track)
	skip := 0

	if *itunesXml != "" {
		f, err := os.Open(*itunesXml)
		defer f.Close()
		if err != nil {
			log.Fatalf("failed to open --itunes_xml: %s", err)
		}
		library, err := itunes.LoadLibrary(f)
		if err != nil {
			log.Fatalf("failed to read library: %s", err)
		}

		trackCount := 0
		for _, v := range library.Tracks {
			loc, _ := url.PathUnescape(v.Location)
			if !strings.HasPrefix(loc, itunesRoot) {
				log.Printf("Warning: Unusual iTunes location: %s `%s`", v.Name, v.Location)
				skip++
				if *skipCount > 0 && skip > *skipCount {
					log.Fatalf("Too many skipped tracks. Failing out...")
				}
				continue
			}

			trackCount++
			loc = strings.ToLower(strings.TrimPrefix(loc, itunesRoot))
			t, ok := tracks[loc]
			if !ok {
				t = &track{}
				tracks[loc] = t
			}
			t.itunesId = v.TrackId
			t.itunesRating = itunesRating(v.Rating)
		}

		log.Printf("iTunes: track count: %d\n", trackCount)
	}

	var c *ampache.Client
	if *ampacheUrl != "" {
		var err error
		c, err = ampache.New(*ampacheUrl)
		if err != nil {
			log.Fatalf("Failed to create Ampache client: %s", err)
		}
		c.WithAuthPassword(ampacheUser, ampachePass)
		c.Verbose = 1

		offset := 0
		trackCount := 0
		for {
			songs, err := c.Songs(map[string]string{"limit": "2000", "offset": strconv.Itoa(offset)})
			if err != nil {
				log.Fatalf("Failed fetching Ampache songs: %s", err)
			}

			for _, s := range songs.Songs {
				if !strings.HasPrefix(s.Filename, ampacheRoot) {
					log.Printf("Warning: Unusual Ampache location: %s `%s`", s.Title, s.Filename)
					skip++
					if *skipCount > 0 && skip > *skipCount {
						log.Fatalf("Too many skipped tracks. Failing out...")
					}
					continue
				}

				trackCount++
				loc := strings.ToLower(strings.TrimPrefix(s.Filename, ampacheRoot))
				t, ok := tracks[loc]
				if !ok {
					t = &track{}
					tracks[loc] = t
				}
				t.ampacheId = s.Id
				t.ampacheRating = ampacheRating(s.Rating)
			}

			if len(songs.Songs) == 0 {
				break
			}

			offset += len(songs.Songs)
		}

		log.Printf("Ampache: track count %d\n", trackCount)
	}

	fmt.Println("== Missing Tracks ==")
	for k, v := range tracks {
		if v.itunesId != 0 && v.ampacheId != 0 {
			continue
		}

		fmt.Printf("%s\n\titunes(%d)\tampache(%d)\n", k, v.itunesId, v.ampacheId)
	}
	fmt.Println("")

	fmt.Println("== Missing Ratings ==")
	for k, v := range tracks {
		if v.itunesId != 0 && v.itunesRating.Equal(v.ampacheRating) {
			continue
		}

		fmt.Printf("%s\n\titunes(%d=%d)\tampache(%d)\n", k, v.itunesRating, v.itunesRating.AsAmpacheRating(), v.ampacheRating)
	}
	fmt.Println("")

	if c != nil && !dryRun {
		fmt.Println("== Copy Ratings To Ampache ==")
		// Pause to give the user a chance to quit.
		time.Sleep(400 * time.Millisecond)

		for k, v := range tracks {
			if v.itunesId != 0 && v.itunesRating.Equal(v.ampacheRating) {
				continue
			}

			if c != nil && !dryRun {
				msg, err := c.Rate(ampache.MediaSong, v.ampacheId, int(v.itunesRating.AsAmpacheRating()))
				if err != nil {
					skip++
					if *skipCount > 0 && skip > *skipCount {
						log.Fatalf("Too many skipped tracks. Failing out...")
					}
				}
			}

		}
	}

}
