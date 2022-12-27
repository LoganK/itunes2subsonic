package main

// Notes:
// -   Normalizes paths to lower case because iTunes/Windows doesn't update if the underlying file changes.
// -   Navidrome requires going into the Player settings and configuring "Report Real Path"

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/delucks/go-subsonic"
	"github.com/logank/itunes2ampache/internal/itunes"
	pb "github.com/schollz/progressbar/v3"
)

var (
	dryRun       = flag.Bool("dry_run", true, "don't modify the library")
	itunesXml    = flag.String("itunes_xml", "", "path to the itunes XML to import")
	skipCount    = flag.Int("skip_count", 10, "a limit on the number of tracks that would be skipped before refusing to process")
	subsonicUrl  = flag.String("subsonic", "", "url of the Subsonic instance")
	updatePlay   = flag.Bool("update_played", true, "update the Last Played time")
	createdFile  = flag.String("created_file", "", "a file to write SQL statements to update the created time")
	itunesRoot   = `file://localhost/M:/Music`
	subsonicRoot = `/music`
)

type itunesRating int

func (r itunesRating) AsSubsonicRating() subsonicRating {
	return subsonicRating(r / 20)
}

func (r itunesRating) Equal(sr subsonicRating) bool {
	return r.AsSubsonicRating() == sr
}

type subsonicRating int

type track struct {
	itunesId       int
	itunesRating   itunesRating
	itunesPlayDate time.Time
	itunesCreated  time.Time

	subsonicId     string
	subsonicRating subsonicRating
}

// PbWithOptions applies options to a progressbar. I like the default, but the
// saucer is something that doesn't render well in my terminal.
func PbWithOptions(p *pb.ProgressBar) *pb.ProgressBar {
	pb.OptionSetTheme(pb.Theme{Saucer: "=", SaucerPadding: " ", BarStart: "[", BarEnd: "]"})(p)
	return p
}

func writeCreatedSql(f io.Writer, tracks map[string]*track) error {
	fmt.Fprintln(f, "# sqlite3 navidrome.db < this_file.sql")
	fmt.Fprintln(f, "# Or if using Docker...")
	fmt.Fprintln(f, "# docker run --rm -i --user 0 -v navidrome_data:/data keinos/sqlite3:latest sqlite3 /data/navidrome.db < this_file.sql")

	// Wrap everything in a transacation so it's not slow.
	fmt.Fprintln(f, "BEGIN TRANSACTION;")
	for _, v := range tracks {
		if v.itunesCreated.IsZero() || v.subsonicId == "" {
			continue
		}

		fmt.Fprintf(f, "UPDATE media_file SET created_at = datetime(%d, 'unixepoch') WHERE id='%s';\n", v.itunesCreated.Unix(), v.subsonicId)
	}
	fmt.Fprintln(f, "COMMIT;")

	return nil
}

func main() {
	flag.Parse()
	subsonicUser, subsonicPass := os.Getenv("SUBSONIC_USER"), os.Getenv("SUBSONIC_PASS")

	if *itunesXml == "" {
		log.Fatal("You must provide --itunes_xml")
	}
	if (subsonicUser != "" || *subsonicUrl != "") && subsonicPass == "" {
		log.Fatal("If connecting to Subsonic, you must set the SUBSONIC_USER and SUBSONIC_PASS environment variables.")
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
			t.itunesPlayDate = v.PlayDateUTC
			t.itunesCreated = v.DateAdded
		}

		log.Printf("iTunes: track count: %d\n", trackCount)
	}

	var c *subsonic.Client
	if *subsonicUrl != "" {
		c = &subsonic.Client{
			Client:     &http.Client{},
			BaseUrl:    *subsonicUrl,
			User:       subsonicUser,
			ClientName: "itunes2subsonic",
		}
		err := c.Authenticate(subsonicPass)
		if err != nil {
			log.Fatalf("Failed to create Subsonic client: %s", err)
		}

		offset := 0
		trackCount := 0
		var bar *pb.ProgressBar
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

			if bar == nil {
				bar = PbWithOptions(pb.Default(int64(len(tracks)), "fetching"))
			}
			for _, s := range songs.Song {
				if !strings.HasPrefix(s.Path, subsonicRoot) {
					log.Printf("Warning: Unusual Subsonic location: %s `%s`", s.Title, s.Path)
					skip++
					if *skipCount > 0 && skip > *skipCount {
						log.Fatalf("Too many skipped tracks. Failing out...")
					}
					continue
				}

				trackCount++
				loc := strings.ToLower(strings.TrimPrefix(s.Path, subsonicRoot))
				t, ok := tracks[loc]
				if !ok {
					t = &track{}
					tracks[loc] = t
				}
				t.subsonicId = s.ID
				t.subsonicRating = subsonicRating(s.UserRating)
			}

			if len(songs.Song) == 0 {
				bar.Finish()
				break
			}

			offset += len(songs.Song)
			bar.Set(offset)
		}

		log.Printf("Subsonic: track count %d\n", trackCount)
	}

	fmt.Println("== Missing Tracks ==")
	for k, v := range tracks {
		if v.itunesId != 0 && v.subsonicId != "" {
			continue
		}

		fmt.Printf("%s\n\titunes(%d)\tsubsonic(%s)\n", k, v.itunesId, v.subsonicId)
	}
	fmt.Println("")

	fmt.Println("== Mismatched Ratings ==")
	var mismatchCount int64 = 0
	for k, v := range tracks {
		if (v.itunesId == 0 || v.subsonicId == "") || v.itunesRating.Equal(v.subsonicRating) {
			continue
		}

		fmt.Printf("%s\n\titunes(%d=%d)\tsubsonic(%d)\n", k, v.itunesRating, v.itunesRating.AsSubsonicRating(), v.subsonicRating)
		mismatchCount++
	}
	fmt.Println("")

	if c != nil && !*dryRun {
		fmt.Printf("== Copy %d Ratings To Subsonic ==\n", mismatchCount)
		// Pause to give the user a chance to quit.
		time.Sleep(400 * time.Millisecond)

		bar := PbWithOptions(pb.Default(mismatchCount, "set rating"))
		for k, v := range tracks {
			if (v.itunesId == 0 || v.subsonicId == "") || v.itunesRating.Equal(v.subsonicRating) {
				continue
			}

			err := c.SetRating(v.subsonicId, int(v.itunesRating.AsSubsonicRating()))
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

	if *updatePlay && !*dryRun {
		bar := PbWithOptions(pb.Default(int64(len(tracks)), "set play time"))
		for k, v := range tracks {
			if v.itunesId == 0 || v.subsonicId == "" || v.itunesPlayDate.IsZero() {
				continue
			}

			err := c.Scrobble(v.subsonicId, map[string]string{
				"time": strconv.Itoa(int(v.itunesPlayDate.UnixMilli())),
			})
			bar.Add(1)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error setting play time for '%s': %s\n", k, err)
				skip++
				if *skipCount > 0 && skip > *skipCount {
					log.Fatalf("Too many skipped tracks. Failing out...")
				}
			}
		}
		bar.Finish()
	}

	if *createdFile != "" {
		f, err := os.OpenFile(*createdFile, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			log.Fatalf("Failed to open given play file: %s", err)
		}
		defer f.Close()

		err = writeCreatedSql(f, tracks)
		if err != nil {
			log.Fatalf("Failed to write play file: %s", err)
		}
	}
}
