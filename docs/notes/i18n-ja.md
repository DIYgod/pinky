# Japanese UI (ja)

Adds `cmd/pinky/locales/ja.json` so Settings › Advanced › Language can pick Japanese, and so a Mac whose preferred languages put Japanese first shows Pinky in Japanese.

## What

- Catalog: every English message key, ICU placeholders unchanged, plurals as `{…, plural, other {…}}` (no one/other split).
- MyGo already ships `locales/ja` for its own menus; `go tool mygo generate cmd/pinky` embeds `ja.json` and registers it with the app languages.
- Tests: `TestTranslations` checks every non-`en` catalog; `TestLanguageMatch` covers `ja` / `ja-JP`; `TestLanguageSetting` expects System + English + Japanese + Simplified Chinese.

## How to try

```sh
go tool mygo generate cmd/pinky
go test ./cmd/pinky/locales ./ui -run 'TestTranslations|TestLanguageMatch|TestLanguageSetting'
MYGO_LOCALE=ja go run ./cmd/pinky --fake
```

In the app: Settings › Advanced › Language › 日本語.

## Style

See "Japanese style" under i18n in [client.md](client.md).
