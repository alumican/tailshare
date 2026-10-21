# tailshare

ファイルやディレクトリを tailnet の中だけに HTTPS で配信して、URL を表示するコマンド。止めると何も残さない。

```sh
tailshare report.html          # 1 ファイルを 1 時間だけ
tailshare -t 0 ./dist          # ビルドしたサイトを Ctrl-C まで
tailshare -t 10m App.ipa       # iPhone 用のインストールページを 10 分だけ
```

## 使い方

```
tailshare [options] <path>

  <path>            ファイル       https://<host>:<port>/<名前>
                    ディレクトリ   https://<host>:<port>/（index.html か、一覧）
                    .ipa          iPhone 用のインストールページ（Install ボタンと manifest）

  -p, --port PORT   tailnet 側の HTTPS ポート（既定：10443, 11443, … のうち空いている最初のもの）
  -t, --time TIME   この時間で止める：90, 30s, 10m, 2h。0 で止めない（既定：1h）
  -q, --quiet       URL 以外は何も出さない
  -h, --help        ヘルプ
  -V, --version     バージョン
```

- 標準出力には URL の1行だけを出す。メッセージとアクセスログは標準エラー（`--quiet` なら出さない）。
- 終了コード：0 は正常（時間切れ・Ctrl-C を含む）、1 はエラー、2 は引数の誤り。
- `tailscale serve` で使われているポートには触らない。`--port` で使用中のポートを指定すると、何もせずに終了する。
- `.ipa` は、開発用か ad hoc で署名されていて、端末の UDID が登録されている必要がある（iOS の制約）。ページの見出しとバージョンは IPA の Info.plist から読む。

## 仕組み

1. 一時ディレクトリに、配信するものへのシンボリックリンクを置く（`.ipa` なら、インストールページと manifest も作る）。元のファイルは動かさない。
2. `python3 -m http.server` を `127.0.0.1` の空いているポートで起動する。
3. `tailscale serve --bg --https=<port>` で、そのポートを tailnet に公開する。
4. 時間切れか Ctrl-C で、`tailscale serve --https=<port> off`、サーバーの停止、一時ディレクトリの削除をして終わる。

macOS の Tailscale アプリはサンドボックスのため、`tailscale serve` でファイルを直接配信できない。そのため、ローカルの HTTP サーバーを経由している。

## 必要なもの

- `tailscale`（tailnet で HTTPS 証明書を有効にしておく）。PATH になければ `/Applications/Tailscale.app` のものを使う。
- `python3`（標準ライブラリだけを使う。3.9 で確認）
- bash

## インストール

```sh
ln -s ~/workspace/tools/tailshare/tailshare ~/.local/bin/tailshare
```
