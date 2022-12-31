# itunes2ampache

Personal project for copying ratings between music instances.

Hiring folks, please don't judge me on this code. 😛

## iTunes -> Subsonic

Copies ratings set in iTunes to a Subsonic server. Safe to run on an ongoing basis (although it cannot sync back to iTunes).

```sh
$ export SUBSONIC_USER=my_user
$ export SUBSONIC_PASS="my subsonic password"
$ go run gitub.com/logank/itunes2subsonic --itunes_xml="iTunes Music Library.xml" --subsonic="https://subsonic.example.com" --dry_run=false
```

## Subsonic -> Subsonic

Copies ratings set in a Subsonic-compatible server to a different Subsonic server. Safe to run on an ongoing basis, but there is insufficient data to identify "newer" ratings so best used to sync in one direction. 

```sh
$ export SUBSONIC_USER=navidrome_user
$ export SUBSONIC_PASS="my navidrome password"
$ export SUBSONIC_USER=ampache_user
$ export SUBSONIC_PASS="my ampache password"
$ go run gitub.com/logank/subsonic2subsonic --subsonic_src="https://navidrome.example.com" --subsonic_dst="https://ampache.example.com" --dry_run=false
```

## iTunes -> Ampache

Copies ratings set in iTunes to an Ampache server. Safe to run on an ongoing basis (although it cannot sync back to iTunes).

> **Note**
> Use itunes2subsonic instead; the Ampache API does not currently provide more advanced support.
