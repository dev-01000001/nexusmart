package policies

import "embed"

// Files embeds the NexusMart Vietnamese policy Markdown corpus for RAG retrieval.
//
//go:embed *.md
var Files embed.FS
