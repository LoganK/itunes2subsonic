package itunes2subsonic

import (
	"testing"
)

func TestLongestLibraryPrefix(t *testing.T) {
	tests := []struct {
		a, b         string
		want1, want2 string
	}{
		{`file://localhost/M:/Music/My%20Music/Rush/2112/01-_2112_.mp3`, `/music/My%20Music/Rush/2112/01-_2112_.mp3`,
			`file://localhost/M:/Music/`, `/music/`},
		{`/music/My%20Music/Rush/2112/01-_2112_.mp3`, `/music/My%20Music/Rush/2112/01-_2112_.mp3`,
			``, ``},
		{`/music/My%20Music/Rush/2112/01-_2112_.mp3`, `/My%20Music/Rush/2112/01-_2112_.mp3`,
			`/music/`, `/`},
		{`/music/My%20Music/Rush/2112/01-_2112_.mp3`, `My%20Music/Rush/2112/01-_2112_.mp3`,
			`/music/`, ``},
		{`/music/My%20Music/Rush/2112/01-_2112_.mp3`, `/music/My%20Music/Rush/2112/02-A_Passage_To_Bangkok.mp3`,
			`/music/My%20Music/Rush/2112/01-_2112_.mp3`, `/music/My%20Music/Rush/2112/02-A_Passage_To_Bangkok.mp3`},
	}

	for _, test := range tests {
		got1, got2 := longestLibraryPrefix(test.a, test.b)
		if got1 != test.want1 {
			t.Errorf("longestCommonSuffix(%s, %s) failed. want1='%s', got1='%s'", test.a, test.b, test.want1, got1)
		}
		if got2 != test.want2 {
			t.Errorf("longestCommonSuffix(%s, %s) failed. want2='%s', got2='%s'", test.a, test.b, test.want2, got2)
		}
	}
}
