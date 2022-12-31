package main

// Notes:
// -   Normalizes paths to lower case because iTunes/Windows doesn't update if the underlying file changes.
// -   Navidrome requires going into the Player settings and configuring "Report Real Path"

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/delucks/go-subsonic"
	i2s "github.com/logank/itunes2subsonic"
	"github.com/logank/itunes2subsonic/internal/itunes"
	pb "github.com/schollz/progressbar/v3"
)

var (
	dryRun       = flag.Bool("dry_run", true, "don't modify the library")
	itunesXml    = flag.String("itunes_xml", "iTunes Music Library.xml", "path to the itunes XML to import")
	skipCount    = flag.Int("skip_count", 10, "a limit on the number of tracks that would be skipped before refusing to process")
	copyUnrated  = flag.Bool("copy_unrated", false, "if true, will unset rating if src is unrated")
	subsonicUrl  = flag.String("subsonic", "", "url of the Subsonic instance")
	updatePlay   = flag.Bool("update_played", true, "update the Last Played time")
	createdFile  = flag.String("created_file", "", "a file to write SQL statements to update the created time")
	itunesRoot   = flag.String("itunes_root", "", "(optional) library prefix for iTunes content")
	subsonicRoot = flag.String("subsonic_root", "", "(optional) library prefix for Subsonic content")
)

type subsonicInfo struct {
	id     string
	path   string
	rating int
}

func (s subsonicInfo) Id() string          { return s.id }
func (s subsonicInfo) Path() string        { return s.path }
func (s subsonicInfo) FiveStarRating() int { return s.rating }

type itunesInfo struct {
	id        int
	path      string
	rating    int
	playDate  time.Time
	dateAdded time.Time
}

func (s itunesInfo) Id() string          { return strconv.Itoa(s.id) }
func (s itunesInfo) Path() string        { return s.path }
func (s itunesInfo) FiveStarRating() int { return s.rating / 20 }

type songPair struct {
	src itunesInfo
	dst subsonicInfo
}

func fetchSubsonicSongs(c *subsonic.Client, bar *pb.ProgressBar) ([]subsonicInfo, error) {
	var tracks []subsonicInfo

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

//func writeNavidromeSql(f io.Writer, tracks map[string]*track) error {
//	fmt.Fprintln(f, "# sqlite3 navidrome.db < this_file.sql")
//	fmt.Fprintln(f, "# Or if using Docker...")
//	fmt.Fprintln(f, "# docker run --rm -i --user 0 -v navidrome_data:/data keinos/sqlite3:latest sqlite3 /data/navidrome.db < this_file.sql")
//
//	// Wrap everything in a transacation so it's not slow.
//	fmt.Fprintln(f, "BEGIN TRANSACTION;")
//	for _, v := range tracks {
//		if v.itunesCreated.IsZero() || v.subsonicId == "" {
//			continue
//		}
//
//		fmt.Fprintf(f, "UPDATE media_file SET created_at = datetime(%d, 'unixepoch') WHERE id='%s';\n", v.itunesCreated.Unix(), v.subsonicId)
//	}
//	fmt.Fprintln(f, "COMMIT;")
//
//	return nil
//}

func main() {
	flag.Parse()
	subsonicUser, subsonicPass := os.Getenv("SUBSONIC_USER"), os.Getenv("SUBSONIC_PASS")

	if (subsonicUser != "" || *subsonicUrl != "") && subsonicPass == "" {
		log.Fatal("If connecting to Subsonic, you must set the SUBSONIC_USER and SUBSONIC_PASS environment variables.")
	}

	var srcSongs []itunesInfo
	if *itunesXml != "" {
		f, err := os.Open(*itunesXml)
		defer f.Close()
		if err != nil {
			log.Fatalf("failed to open --itunes_xml=%s: %s", *itunesXml, err)
		}
		library, err := itunes.LoadLibrary(f)
		if err != nil {
			log.Fatalf("failed to read library: %s", err)
		}

		for _, v := range library.Tracks {
			loc, err := url.PathUnescape(v.Location)
			if err != nil {
				log.Fatalf("Unexpected iTunes location '%s': %s", v.Location, err)
			}

			srcSongs = append(srcSongs, itunesInfo{
				id:        v.TrackId,
				path:      loc,
				rating:    v.Rating,
				playDate:  v.PlayDateUTC,
				dateAdded: v.DateAdded,
			})
		}
	}

	c := &subsonic.Client{
		Client:         &http.Client{},
		BaseUrl:        *subsonicUrl,
		User:           subsonicUser,
		ClientName:     "itunes2subsonic",
		RequireDotView: true,
	}
	if err := c.Authenticate(subsonicPass); err != nil {
		log.Fatalf("Failed to create Subsonic client: %s", err)
	}

	fetchBar := i2s.PbWithOptions(pb.Default(-1, "fetching subsonic data"))
	dstSongs, err := fetchSubsonicSongs(c, fetchBar)
	if err != nil {
		log.Fatalf("Failed fetching subsonic songs: %s", err)
	}

	log.Printf("Src track count %d, Dst track count %d\n", len(srcSongs), len(dstSongs))

	if *itunesRoot == "" && *subsonicRoot == "" {
		s := make([]i2s.SongInfo, 0, len(srcSongs))
		for _, si := range srcSongs {
			s = append(s, si)
		}
		d := make([]i2s.SongInfo, 0, len(dstSongs))
		for _, si := range dstSongs {
			d = append(d, si)
		}
		*itunesRoot, *subsonicRoot = i2s.LibraryPrefix(s, d)
	}
	fmt.Printf("Music library root: src='%s' dst='%s'\n", *itunesRoot, *subsonicRoot)

	byPath := make(map[string]*songPair)
	for _, s := range srcSongs {
		p := strings.TrimPrefix(strings.ToLower(s.Path()), *itunesRoot)
		t, ok := byPath[p]
		if !ok {
			t = &songPair{}
			byPath[p] = t
		}
		t.src = s
	}
	for _, s := range dstSongs {
		p := strings.TrimPrefix(strings.ToLower(s.Path()), *subsonicRoot)
		t, ok := byPath[p]
		if !ok {
			t = &songPair{}
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
* Set --itunes_root and --subsonic_root to the correct values
* In Navidrome Player Settings, configure "Report Real Path"\n`)
	}

	fmt.Println("== Mismatched Ratings ==")
	var mismatchCount int64 = 0
	for k, v := range byPath {
		if v.src.Id() == "" || v.dst.Id() == "" || v.src.FiveStarRating() == v.dst.FiveStarRating() {
			continue
		}
		if v.src.FiveStarRating() == 0 && !*copyUnrated {
			continue
		}

		fmt.Printf("%s\n\trating src(%d)\tdst(%d)\n", k, v.src.FiveStarRating(), v.dst.FiveStarRating())
		mismatchCount++
	}
	fmt.Println("")

	fmt.Printf("== Copy %d Ratings To Subsonic ==\n", mismatchCount)
	if *dryRun {
		fmt.Printf("Set --dry_run=false to modify %s", *subsonicUrl)
	} else {
		fmt.Printf("== Copy %d Ratings To Subsonic ==\n", mismatchCount)
		// Pause to give the user a chance to quit.
		time.Sleep(400 * time.Millisecond)

		skip := 0
		bar := i2s.PbWithOptions(pb.Default(mismatchCount, "set rating"))
		for k, v := range byPath {
			if v.src.Id() == "" || v.dst.Id() == "" || v.src.FiveStarRating() == v.dst.FiveStarRating() {
				continue
			}
			if v.src.FiveStarRating() == 0 && !*copyUnrated {
				continue
			}

			err := c.SetRating(v.dst.Id(), v.src.FiveStarRating())
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

	//	if *updatePlay && !*dryRun {
	//		bar := PbWithOptions(pb.Default(int64(len(tracks)), "set play time"))
	//		for k, v := range tracks {
	//			if v.itunesId == 0 || v.subsonicId == "" || v.itunesPlayDate.IsZero() {
	//				continue
	//			}
	//
	//			err := c.Scrobble(v.subsonicId, map[string]string{
	//				"time": strconv.Itoa(int(v.itunesPlayDate.UnixMilli())),
	//			})
	//			bar.Add(1)
	//			if err != nil {
	//				fmt.Fprintf(os.Stderr, "Error setting play time for '%s': %s\n", k, err)
	//				skip++
	//				if *skipCount > 0 && skip > *skipCount {
	//					log.Fatalf("Too many skipped tracks. Failing out...")
	//				}
	//			}
	//		}
	//		bar.Finish()
	//	}
	//
	//	if *createdFile != "" {
	//		f, err := os.OpenFile(*createdFile, os.O_RDWR|os.O_CREATE, 0644)
	//		if err != nil {
	//			log.Fatalf("Failed to open given play file: %s", err)
	//		}
	//		defer f.Close()
	//
	//		err = writeCreatedSql(f, tracks)
	//		if err != nil {
	//			log.Fatalf("Failed to write play file: %s", err)
	//		}
	//	}
}
