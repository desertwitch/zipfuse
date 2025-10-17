package main

import (
	"slices"
	"testing"
)

// Expectation: The expected command should be built from the given arguments.
//
//nolint:maintidx
func Test_MountHelper_BuildCommand_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr bool
	}{
		{
			name: "basic mount no options",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b"},
		},
		{
			name: "bare flag option",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "allow-other"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other"},
		},
		{
			name: "key=value option",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "webserver=:8000"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--webserver", ":8000"},
		},
		{
			name: "mixed bare flag and key=value",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "allow-other,stream-threshold=2MiB"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other", "--stream-threshold", "2MiB"},
		},
		{
			name: "options separated by dashes",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "allow-other,verbose,fd-cache-ttl=3600"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other", "--fd-cache-ttl", "3600", "--verbose"},
		},
		{
			name: "options with prefix and dashes",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "--allow-other,--verbose,--fd-cache-ttl=3600"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other", "--fd-cache-ttl", "3600", "--verbose"},
		},
		{
			name: "multiple options",
			args: []string{
				"mount.zipfuse",
				"/mnt/a",
				"/mnt/b",
				"allow-other,webserver=:9000,stream-threshold=10MiB",
			},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other", "--stream-threshold", "10MiB", "--webserver", ":9000"},
		},
		{
			name: "from basename mount.fuse.zipfuse",
			args: []string{"mount.fuse.zipfuse", "/mnt/a", "/mnt/b"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b"},
		},
		{
			name: "from basename mount.fuseblk.ntfs",
			args: []string{"mount.fuseblk.ntfs", "/mnt/a", "/mnt/b"},
			want: []string{"ntfs", "/mnt/a", "/mnt/b"},
		},
		{
			name: "derived from source# syntax",
			args: []string{"mount.fuseblk.", "zipfuse#/path/archive", "/mnt/b"},
			want: []string{"zipfuse", "/path/archive", "/mnt/b"},
		},
		{
			name: "explicit -t fuse.zipfuse",
			args: []string{"mount", "/mnt/a", "/mnt/b", "-t", "fuse.zipfuse"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b"},
		},
		{
			name: "explicit -t fuseblk.ntfs",
			args: []string{"mount", "/mnt/a", "/mnt/b", "-t", "fuseblk.ntfs"},
			want: []string{"ntfs", "/mnt/a", "/mnt/b"},
		},
		{
			name: "explicit -t without fuse/fuseblk prefix",
			args: []string{"mount", "/mnt/a", "/mnt/b", "-t", "zipfuse"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b"},
		},
		{
			name: "options passed without -o",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "allow-other,webserver=:8080"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other", "--webserver", ":8080"},
		},
		{
			name: "options passed with -o",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "-o", "allow-other,webserver=:8080"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other", "--webserver", ":8080"},
		},
		{
			name: "multiple -o flags merged",
			args: []string{
				"mount.zipfuse", "/mnt/a", "/mnt/b",
				"-o", "allow-other", "-o", "webserver=:7000",
			},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other", "--webserver", ":7000"},
		},
		{
			name: "ignore -v flag",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "-v", "allow-other"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other"},
		},
		{
			name: "multiple -v flags anywhere",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "-v", "-v", "-v"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b"},
		},
		{
			name: "underscore converted to dash in bare option",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "allow_other"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other"},
		},
		{
			name: "underscore converted to dash in key=value",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "stream_threshold=256"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--stream-threshold", "256"},
		},
		{
			name: "multiple underscores converted",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "stream_pool_size"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--stream-pool-size"},
		},
		{
			name: "fd_cache_ttl option",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "fd_cache_ttl=60"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--fd-cache-ttl", "60"},
		},
		{
			name: "fd_cache_size option",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "fd_cache_size=1024"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--fd-cache-size", "1024"},
		},
		{
			name: "ring_buffer_size option",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "ring_buffer_size=8192"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--ring-buffer-size", "8192"},
		},
		{
			name: "option value with space",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "stream-threshold=128 MiB"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--stream-threshold", "128 MiB"},
		},
		{
			name: "source with space",
			args: []string{"mount.zipfuse", "/mnt/with space", "/mnt/b"},
			want: []string{"zipfuse", "/mnt/with space", "/mnt/b"},
		},
		{
			name: "mountpoint with space",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/with space"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/with space"},
		},
		{
			name: "option value with special chars",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "webserver=pa$$:&word"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--webserver", "pa$$:&word"},
		},
		{
			name: "empty option string ignored",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "allow-other,,verbose"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other", "--verbose"},
		},
		{
			name: "empty -o argument ignored",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "-o"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b"},
		},
		{
			name: "unknown option ignored",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "unknown-option,allow-other"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other"},
		},
		{
			name: "options alphabetically sorted",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "webserver=:8080,allow-other,dry-run"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other", "--dry-run", "--webserver", ":8080"},
		},
		{
			name: "source#type with path containing colon",
			args: []string{"mount.fuseblk.", "zipfuse#/path:with:colons", "/mnt/b"},
			want: []string{"zipfuse", "/path:with:colons", "/mnt/b"},
		},
		{
			name: "source#type with multiple hashes uses first",
			args: []string{"mount.fuseblk.", "zipfuse#/path#with#hashes", "/mnt/b"},
			want: []string{"zipfuse", "/path#with#hashes", "/mnt/b"},
		},
		{
			name: "type from basename overrides default",
			args: []string{"mount.fuse.zipfuse", "/mnt/a", "/mnt/b"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b"},
		},
		{
			name: "explicit -t overrides basename",
			args: []string{"mount.fuse.zipfuse", "/mnt/a", "/mnt/b", "-t", "ntfs"},
			want: []string{"ntfs", "/mnt/a", "/mnt/b"},
		},
		{
			name: "source#type with -t flag, -t wins",
			args: []string{"mount", "zipfuse#/path", "/mnt/b", "-t", "ntfs"},
			want: []string{"ntfs", "zipfuse#/path", "/mnt/b"},
		},
		{
			name: "-o option followed by more args",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "-o", "allow-other", "extra-arg"},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--allow-other"},
		},
		{
			name: "empty value in key= option",
			args: []string{"mount.zipfuse", "/mnt/a", "/mnt/b", "webserver="},
			want: []string{"zipfuse", "/mnt/a", "/mnt/b", "--webserver"},
		},
		{
			name: "numeric type from -t",
			args: []string{"mount", "/mnt/a", "/mnt/b", "-t", "123"},
			want: []string{"123", "/mnt/a", "/mnt/b"},
		},
		{
			name: "root paths with trailing slashes",
			args: []string{"mount.zipfuse", "/mnt/a/", "/mnt/b/"},
			want: []string{"zipfuse", "/mnt/a/", "/mnt/b/"},
		},
		{
			name: "relative paths",
			args: []string{"mount.zipfuse", "./source", "./dest"},
			want: []string{"zipfuse", "./source", "./dest"},
		},
		{
			name: "explicit binary path",
			args: []string{"mount.zipfuse", "./source", "./dest", "-o", "mbin=/bin/zipfuze"},
			want: []string{"/bin/zipfuze", "./source", "./dest"},
		},
		{
			name:    "explicit -t fuse. with empty suffix errors",
			args:    []string{"mount", "/mnt/a", "/mnt/b", "-t", "fuse."},
			wantErr: true,
		},
		{
			name:    "explicit -t fuseblk. with empty suffix errors",
			args:    []string{"mount", "/mnt/a", "/mnt/b", "-t", "fuseblk."},
			wantErr: true,
		},
		{
			name:    "source with only # gives empty type error",
			args:    []string{"mount.fuseblk.", "#/mnt/a", "/mnt/b"},
			wantErr: true,
		},
		{
			name:    "source with only # gives empty source error",
			args:    []string{"mount.fuseblk.", "zipfuse#", "/mnt/b"},
			wantErr: true,
		},
		{
			name:    "empty source argument",
			args:    []string{"mount.zipfuse", "", "/mnt/b"},
			wantErr: true,
		},
		{
			name:    "empty mountpoint argument",
			args:    []string{"mount.zipfuse", "/mnt/a", ""},
			wantErr: true,
		},
		{
			name:    "source without # in generic mount helper",
			args:    []string{"mount.fuseblk.", "nosource", "/mnt/b"},
			wantErr: true,
		},
		{
			name:    "missing -t value",
			args:    []string{"mount", "/mnt/a", "/mnt/b", "-t"},
			wantErr: true,
		},
		{
			name:    "invalid mtmo value",
			args:    []string{"mount", "/mnt/a", "/mnt/b", "-o", "mtmo=0"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mh, err := newMountHelper(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewMountHelper() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			got := mh.BuildCommand()
			if !slices.Equal(got, tt.want) {
				t.Errorf("BuildCommand() = %v\nwant %v", got, tt.want)
			}
		})
	}
}
