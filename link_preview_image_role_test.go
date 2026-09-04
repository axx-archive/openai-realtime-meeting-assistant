package main

import (
	"net/url"
	"testing"
)

func TestXPostImageRoleKeepsSharedMediaOutOfAuthorAvatar(t *testing.T) {
	base, _ := url.Parse("https://x.com/example/status/12345")
	for _, tt := range []struct{ image, role string }{
		{"https://pbs.twimg.com/media/example.jpg", "content"},
		{"https://pbs.twimg.com/profile_images/123/avatar.jpg", "author_avatar"},
		{"https://example.com/profile_images/123/avatar.jpg", "content"},
	} {
		preview := parseLinkPreviewHTML(base, []byte(`<meta property="og:image" content="`+tt.image+`">`))
		if preview.ImageURL != tt.image || preview.ImageRole != tt.role {
			t.Fatalf("image %q: got role %q and URL %q", tt.image, preview.ImageRole, preview.ImageURL)
		}
	}
}
