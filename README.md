# tailshare

ファイルやディレクトリを tailnet の中だけに HTTPS で配信して、URL を表示するコマンド。止めると何も残さない。
何をどう配信するかは、パスの種類ごとの**アダプタ**が決める。アダプタはディレクトリを置くだけで増やせる。

```sh
tailshare report.html          # ページと、そこから参照しているファイルを 1 時間だけ
tailshare -t 0 ./dist          # ビルドしたサイトを Ctrl-C まで
tailshare -t 10m App.ipa       # iPhone 用のインストールページを 10 分だけ
```

## 使い方

```
tailshare [options] <path>
tailshare --adapters
tailshare --which <path>

  -p, --port PORT   tailnet 側の HTTPS ポート（既定：10443, 11443, … のうち空いている最初のもの）
  -t, --time TIME   この時間で止める：90, 30s, 10m, 2h。0 で止めない（既定：1h）
  -a, --as NAME     パスにかかわらず、このアダプタを使う（例：--as file）
  -q, --quiet       URL 以外は何も出さない
      --adapters    アダプタを、試す順に一覧する
      --which PATH  PATH をどのアダプタが配信するかを表示する（配信はしない）
  -h, --help / -V, --version
```

- 標準出力には URL の1行だけを出す。メッセージとアクセスログは標準エラー（`--quiet` なら出さない）。
- 終了コード：0 は正常（時間切れ・Ctrl-C を含む）、1 はエラー、2 は引数の誤り。
- `tailscale serve` で使われているポートには触らない。`--port` で使用中のポートを指定すると、何もせずに終了する。

## 付属のアダプタ

| アダプタ | 対象 | 配信するもの |
|---|---|---|
| `10-ipa` | `*.ipa` | iPhone 用のインストールページ（Install ボタンと manifest）と IPA。見出しとバージョンは IPA の Info.plist から読む。テンプレートは `index.html` と `manifest.plist` |
| `50-html` | `*.html` `*.htm` | そのページと、ページが参照しているローカルのファイルだけ（`src`・`href`・`srcset`・`poster`・スタイルの `url()` と `@import`。CSS の先もたどる）。同じディレクトリのほかのファイルや、ディレクトリの外は出さない |
| `80-dir` | ディレクトリ | ディレクトリを `/` に。`index.html` があればそれ、なければ一覧。先頭が `.` のもの（`.git`、`.env` など）は出さない |
| `99-file` | それ以外すべて | そのファイルだけを `/<名前>` に |

- `.ipa` は、開発用か ad hoc で署名されていて、端末の UDID が登録されている必要がある（iOS の制約）。
- `50-html` は、スクリプトが自分で読み込むファイル（`fetch` など）までは見つけられない。そういうページは、ディレクトリごと配信する（`tailshare ./site`）。

## アダプタの足し方

アダプタは、次の2つを置いたディレクトリ。置けば次の回から使われる（登録は要らない）。

```
30-apk/
├── match     # 対象のパターン（空白か改行で区切る。# 以降はコメント）
└── prepare   # 実行権限つきのスクリプト（言語は自由）。テンプレートなど、ほかのファイルも同じ場所に置ける
```

- **置き場所**：`~/.config/tailshare/adapters/`（自分用）と、tailshare 本体の `adapters/`（付属）。同じ名前があれば自分用が優先する。`TAILSHARE_ADAPTER_PATH`（`:` 区切り）で探す場所を変えられる。
- **振り分け**：名前順に試して、`match` に最初に合ったものを使う。先頭の番号が優先順位になる。`--as` では、番号を除いた名前（`ipa`）でも、ディレクトリ名（`10-ipa`）でも指定できる。
- **パターン**：パスの名前に対するシェルのグロブ（大文字と小文字は区別しない）。ディレクトリにはグロブは当てはまらず、`@dir` だけが当てはまる。
- `prepare` に実行権限がないアダプタは、警告を出して飛ばす（`--adapters` にも表示される）。

### `prepare` の約束事

```
prepare <path> <site> <base-url>
```

| | |
|---|---|
| `<path>` | 配信するもの（絶対パス） |
| `<site>` | 配信するディレクトリ（空）。ここに置いたものが `<base-url>/` 以下で見える。元のファイルはコピーせず、シンボリックリンクを置く |
| `<base-url>` | `https://<host>:<port>`。manifest のように、絶対 URL が要るときに使う |
| 環境変数 `TAILSHARE_ADAPTER_DIR` | アダプタ自身のディレクトリ（テンプレートを読むときに使う） |
| 標準出力 | 最後の1行に、開くパス（`/` や `/report.html`）を書く。URL エンコードは本体がする |
| 終了コード | 0 以外なら、本体は配信せずに終わる。理由は標準エラーに書く |

例（`99-file/prepare` の全体）：

```bash
#!/bin/bash
set -euo pipefail
input="$1" site="$2"
name="$(basename "$input")"
ln -s "$input" "$site/$name"
echo "/$name"
```

## 仕組み

1. アダプタが、一時ディレクトリに配信するものを用意する。
2. `python3 -m http.server` を `127.0.0.1` の空いているポートで起動する。
3. `tailscale serve --bg --https=<port>` で、そのポートを tailnet に公開する。
4. 時間切れか Ctrl-C で、`tailscale serve --https=<port> off`、サーバーの停止、一時ディレクトリの削除をして終わる。

macOS の Tailscale アプリはサンドボックスのため、`tailscale serve` でファイルを直接配信できない。そのため、ローカルの HTTP サーバーを経由している。

## 必要なもの

- `tailscale`（tailnet で HTTPS 証明書を有効にしておく）。PATH になければ `/Applications/Tailscale.app` のものを使う。
- `python3`（標準ライブラリだけを使う。3.9 で確認）
- bash（macOS 標準の 3.2 で動く）

## インストール

```sh
ln -s ~/workspace/tools/tailshare/tailshare ~/.local/bin/tailshare
```
