package config

import (
	"reflect"
	"testing"
)

func TestClone_nil(t *testing.T) {
	if Clone(nil) != nil {
		t.Fatal("Clone(nil) should be nil")
	}
}

func TestClone_deepCopiesSlicesAndPointers(t *testing.T) {
	threads := 4
	nested := 2
	src := &Config{
		SourceDirs:                        []string{"/a", "/b"},
		RecursiveMountExtensions:          []string{".zip"},
		Convert7zFlattenExclude:           []string{"x"},
		ExtraRatarmountArgs:               []string{"--foo"},
		ArchiveconverterExcludeInner:      []string{"i"},
		ArchiveconverterExcludeOuter:      []string{"o"},
		ArchiveconverterRename:            []string{"r"},
		ArchiveconverterExtraArgs:         []string{"e"},
		UnknownKeys:                       []string{"u"},
		ArchiveconverterThreads:           &threads,
		ArchiveconverterNestedConcurrency: &nested,
		LogLevel:                          "INFO",
		WebPort:                           8788,
	}
	dst := Clone(src)
	if dst == nil || dst == src {
		t.Fatal("Clone must return a new non-nil pointer")
	}
	if !reflect.DeepEqual(src, dst) {
		t.Fatalf("clone content mismatch:\n src=%+v\n dst=%+v", src, dst)
	}

	// Mutate source slices / pointers; clone must stay independent.
	src.SourceDirs[0] = "/mutated"
	src.SourceDirs = append(src.SourceDirs, "/c")
	src.LogLevel = "DEBUG"
	*src.ArchiveconverterThreads = 99
	*src.ArchiveconverterNestedConcurrency = 99
	src.UnknownKeys[0] = "changed"

	if dst.SourceDirs[0] != "/a" || len(dst.SourceDirs) != 2 {
		t.Fatalf("SourceDirs not independent: %v", dst.SourceDirs)
	}
	if dst.LogLevel != "INFO" {
		t.Fatalf("LogLevel not independent: %s", dst.LogLevel)
	}
	if dst.ArchiveconverterThreads == nil || *dst.ArchiveconverterThreads != 4 {
		t.Fatalf("Threads not independent: %v", dst.ArchiveconverterThreads)
	}
	if dst.ArchiveconverterNestedConcurrency == nil || *dst.ArchiveconverterNestedConcurrency != 2 {
		t.Fatalf("NestedConcurrency not independent: %v", dst.ArchiveconverterNestedConcurrency)
	}
	if dst.UnknownKeys[0] != "u" {
		t.Fatalf("UnknownKeys not independent: %v", dst.UnknownKeys)
	}
}

func TestClone_nilSlicesAndPointers(t *testing.T) {
	src := &Config{LogLevel: "WARNING"}
	dst := Clone(src)
	if dst.SourceDirs != nil {
		t.Fatalf("nil SourceDirs should stay nil, got %v", dst.SourceDirs)
	}
	if dst.ArchiveconverterThreads != nil {
		t.Fatalf("nil Threads should stay nil")
	}
}
