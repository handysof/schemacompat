# schemacompat

A Go package that checks whether a new version of a JSON Schema is backwards-compatible with the current one.

## Install

```
go get github.com/handysof/schemacompat
```

## Usage

```go
import "github.com/handysof/schemacompat"

err := schemacompat.Check(currentSchema, nextSchema, schemacompat.Options{})
if err != nil {
    // err lists every breaking change
}
```

## What it allows
- Adding optional fields
- Relaxing constraints (lower minLength, higher maxLength, etc.)
- Widening types (integer to number)
- Deprecating fields
- Adding enum options

## What it rejects
- Removing fields
- Changing field types
- Making fields required
- Tightening constraints
- Adding patterns or formats

## License

MIT © HandySof Technologies
