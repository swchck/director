# 08 — Translations

Demonstrates working with multi-language content: fetching translated items from Directus and querying them by language in memory.

**What you'll learn:**
- `FindTranslation` and `FindTranslationWithFallback` helpers
- `TranslationMap` for O(1) language lookups
- `TranslatedView` for per-language flattened views
- Fetching translations via `WithFields("*", "translations.*")`

```bash
go run example/08-translations/main.go
```

No infrastructure needed — this example uses in-memory data only.
