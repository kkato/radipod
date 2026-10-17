# radipod

radikoを自宅サーバーに録音し、Androidの[AntennaPod](https://antennapod.org/)で聴くためのGo製ツール。

```text
radiko → M4A保存 → RSS配信 → AntennaPodでダウンロード・再生
```

番組名を設定すると定期的に取得し、録音済みの番組はスキップします。
**エリア内・過去7日以内のタイムフリー**に対応。プレミアムログインとリアルタイム録音は未対応です。

## 1. 録音対象を設定

初回のみ、[設定例](config.example.json)をコピーします。既存の設定がある場合はそのまま使えます。

```sh
cp -n config.example.json config.local.json
```

`config.local.json` の次の項目を編集します。

| 項目 | 設定例 |
| --- | --- |
| `base_url` | スマホから届くサーバーのURL。例: `http://192.168.1.10:8080` |
| `title` | AntennaPodに表示する名前。例: `自分のラジオ` |
| `lookback_days` | 検索する放送日数。週1回の番組なら `7` |
| `rules` | 放送局IDと番組名の部分一致。下の例を参照 |

「安住紳一郎の日曜天国」を録音する例:

```json
"rules": [
  { "station": "TBS", "match": "日曜天国" }
]
```

## 2. 起動

macOS / Linuxで、Go 1.26以上とFFmpeg 4.3以上・ffprobeを用意します。

```sh
make build

# 対象を確認（録音はしない）
./bin/radipod sync -config config.local.json -dry-run

# 定期録音とRSS配信を開始
./bin/radipod run -config config.local.json
```

起動直後に取得し、以降は既定で30分おきに確認します。設定変更後は再起動してください。
音声は `data_dir` に保存します。相対パスは設定ファイルの場所が基準です。

### Docker Composeを使う場合

ホストにGo・FFmpegは不要です。設定の `data_dir` を `./data`、`listen` を `0.0.0.0:8080` にします。

```sh
docker compose up -d --build
docker compose logs -f --tail=100
```

音声は永続ボリュームに保存されます。停止は `docker compose down`。音声を残す場合は `-v` を付けないでください。
ポートやBasic認証の設定は [.env.example](.env.example) を参照してください。

## 3. AntennaPodで聴く

AntennaPodに **`base_url` + `/feed.xml`** を登録します。
例: `http://192.168.1.10:8080/feed.xml`

自宅Wi-Fiでダウンロードすれば、通勤中はオフラインで再生できます。
外出先でストリーミングする場合はHTTPSで公開し、[Basic認証](.env.example)を設定します。AntennaPodにも同じユーザー名・パスワードを登録してください。
`127.0.0.1` や `localhost` はスマホからサーバーに接続するアドレスとしては使えません。

## その他の操作

```sh
./bin/radipod stations                               # 放送局一覧
./bin/radipod programs -station TBS -date yesterday   # 番組表
./bin/radipod sync -config config.local.json          # 1回だけ録音
./bin/radipod record -h                              # 日時を指定して録音
```

日時は日本時間です。放送日は05:00に切り替わり、`yesterday` は前の放送日を指します。
radikoの非公式仕様を利用しているため、仕様変更時には修正が必要です。

## 開発

```sh
make check
go test -race ./...
```

テストは実際のradikoに接続せず、HTTPモックと合成音声を使います。
参考実装のライセンスは [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) を参照してください。
