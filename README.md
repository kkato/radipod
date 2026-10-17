# radipod

radikoの音声を自宅サーバーに保存し、自分用のRSSを通してAndroidのAntennaPodで聴くためのGo製ツール。

```text
radiko → radipodで取得 → 自宅サーバーに音声・番組情報を保存
                                      ↓
                               RSS・音声をHTTP配信
                                      ↓
                           AntennaPodで取得して通勤中に再生
```

## できること

- 接続元エリアの放送局一覧・番組表の取得
- 過去7日以内のタイムフリーを指定してM4Aに保存
- 番組名の部分一致で放送済み番組をまとめて保存
- 同じ放送局・開始日時・終了日時の重複保存を防止
- RSS 2.0と音声のHTTP配信（Range・HEAD対応）
- RSSと音声の両方に任意のBasic認証を設定
- 設定ファイルの番組名ルールに従って定期取得しながらRSSを配信
- `sync -dry-run` で録音前に対象を確認

現在はエリア内・ログインなしの取得に対応する。プレミアムログイン、エリアフリー、タイムフリー30、リアルタイム録音、Web予約画面は未実装。
radikoとの通信には非公式の配信仕様を使用するため、サービス側の変更時は追従が必要。

## 必要なもの

- ビルド: Go 1.26以上
- 録音: FFmpeg 4.3以上とffprobe（AAC・HLS対応、PATHに配置）
- 実行先: macOSまたはLinux、自宅サーバーからradikoに接続できること

外部のGoモジュールやDBは使用しない。配信のみならFFmpegは不要。

```sh
make build
./bin/radipod -h
```

## 1. 放送局と番組を調べる

```sh
./bin/radipod stations
./bin/radipod programs -station LFR -date yesterday
./bin/radipod programs -station LFR -date 2026-10-16
```

番組表は「開始日時・終了日時・番組名」の順で表示する。
日時はすべて日本時間。`-date` はradikoの放送日（当日05:00〜翌日05:00）を指定する。
`today` は実行時点が属する放送日、`yesterday` はその前の放送日。
たとえば10月17日01:00の `today` は10月16日、`yesterday` は10月15日になる。

## 2. 録音する

番組表に出た開始日時を渡す。次の日時は例なので、取得可能な番組に置き換える。

```sh
./bin/radipod record \
  -station LFR \
  -start 20261016010000 \
  -data ./data
```

終了日時とタイトルは番組表から補完する。`-start` を番組の途中にすると、その位置から保存する。
短い範囲を試す場合は、同じ番組内の `-end` と任意の `-title` を指定できる。

```sh
./bin/radipod record \
  -station LFR \
  -start 20261016010000 \
  -end 20261016010100 \
  -title '接続確認（1分）' \
  -data ./data
```

番組名の部分一致でも取得できる。分割された番組は該当する各枠を別エピソードとして保存する。

```sh
./bin/radipod record \
  -station LFR \
  -date yesterday \
  -match 'オールナイトニッポン' \
  -data ./data
```

- 配信反映を待つため、取得対象の終了から3分以上経過した音声を対象にする。
- `-match` では未終了の枠を飛ばす。終了済みの対象が0件なら終了コード1。
- 保存済みの対象はスキップし、成功として扱う。
- 対象番組がタイムフリー不可ならエラーにする。
- 取得は最大5分ずつ行い、失敗時に別の配信URLと再試行を使う。
- FFprobeで音声と時間長を確認し、不完全な録音は公開しない。ただし音声内容の欠落や差し替えを完全に判定するものではない。
- 1件のタイムアウトは既定30分。必要に応じて `-timeout 1h` などを指定する。

## 3. RSSと音声を配信する

`192.168.1.10` は自宅サーバーのLANアドレスに置き換える。

```sh
./bin/radipod serve \
  -data ./data \
  -listen 0.0.0.0:8080 \
  -base-url http://192.168.1.10:8080 \
  -title '自分のラジオ'
```

AntennaPodで `http://192.168.1.10:8080/feed.xml` を購読する。
自宅Wi-Fiでダウンロード後、通勤中はオフラインで再生できる。
配信と録音は別プロセスで実行でき、録音完了後の番組は次のRSS取得時に反映する。

`-listen` の既定値は `127.0.0.1:8080`。スマホから直接接続する場合は上の例のように変更する。
`-base-url` はスマホから届くHTTP(S)のオリジンを指定する。パス付きURLや認証情報入りURLは受け付けない。

任意で `RADIPOD_HTTP_USER` と `RADIPOD_HTTP_PASSWORD` の両方を環境変数に設定するとBasic認証が有効になる。
パスワードはRSSや音声URLには含めない。Basic認証を使って外部から接続する場合は、HTTPS終端をリバースプロキシ側に用意する。
認証を設定しないLAN配信から始め、ルーターのポート開放は不要。

RSSだけを標準出力に出すこともできる。

```sh
./bin/radipod feed -data ./data -base-url http://192.168.1.10:8080 > feed.xml
```

`feed` はXMLを出力するだけで、音声配信やRSSの定期更新は行わない。通常は `serve` を使用する。

## 毎日の取得

常用する場合は、設定ファイルと `run` を使う。録音ルールには、放送局と番組名の部分一致を複数登録できる。

```sh
cp config.example.json config.local.json
```

`config.local.json` の `base_url` をスマホから届く自宅サーバーのアドレスに、`rules` の `match` を録音したい番組名に変更する。
サンプルの「録音したい番組名の一部」は置き換え用で、そのままでは通常どの番組にも一致しない。

```sh
# 音声を取得せず、保存対象だけを表示する（FFmpeg不要）
./bin/radipod sync -config config.local.json -dry-run

# 対象を1回だけ取得する
./bin/radipod sync -config config.local.json

# HTTP配信と定期取得を一緒に開始する
./bin/radipod run -config config.local.json
```

`run` は起動直後に対象を検索・取得し、その巡回が終わってから `poll_interval`（既定30分）待つ。
同じプロセス内で録音ジョブは重ならない。通信失敗があってもRSS配信は続け、次の巡回で未保存の番組を再試行する。
起動・再起動時も過去の放送日を調べるので、停止中に放送された番組を検索範囲内で取り戻せる。

| 設定 | 意味 |
| --- | --- |
| `data_dir` | 保存先。相対パスは**設定ファイルがあるディレクトリ**を基準にする |
| `listen` | HTTP待受アドレス。例: `0.0.0.0:8080` |
| `base_url` | スマホから届く配信元URL。例: `http://192.168.1.10:8080` |
| `title` | AntennaPodに表示する購読名 |
| `poll_interval` | 巡回の間隔。`1m`〜`24h`、既定 `30m` |
| `lookback_days` | 当放送日を含む検索日数。1〜7、既定2（当放送日と前放送日） |
| `record_timeout` | 番組1件あたりの取得制限時間。既定 `30m` |
| `rules` | `station` と `match` の組。番組名は大文字・小文字を区別しない部分一致 |

複数ルールに一致しても保存は1回。分割番組は各枠ごとに保存し、タイムフリー不可・未終了・配信反映待ちの枠は飛ばす。
自動取得で該当なしは正常終了として扱う。存在しないエリア外の局やAPI取得失敗はエラーとして報告し、ほかの局の取得は続ける。
一時停止が2放送日を超える場合、再起動前に `lookback_days` を増やしておく（最大7）。設定変更は再起動後に反映する。
`rules: []` にすると自動録音せずRSS配信だけ動かせる。

cronで既存の単発コマンドを呼ぶ運用も可能。サーバーのタイムゾーンをAsia/Tokyoにし、毎朝06:15に前の放送日を取得する例:

```cron
PATH=/usr/local/bin:/usr/bin:/bin
15 6 * * * /opt/radipod/bin/radipod record -station LFR -date yesterday -match オールナイトニッポン -data /srv/radipod >> /srv/radipod/record.log 2>&1
```

実行ファイル・保存先・FFmpegのPATHを実環境に合わせる。`/srv/radipod` は事前に作成し、実行ユーザーが書き込めるようにする。
macOSのHomebrewならPATHに `/opt/homebrew/bin` も必要。
cronのインストールは自動では行わない。`run` と同じ対象を重複スケジュールする必要はない。

## Docker Composeで起動

Docker EngineとComposeが動く自宅サーバーで実行する。
先に上記の `config.local.json` を用意し、`data_dir` は `./data`、`listen` は `0.0.0.0:8080` にする。
設定ファイルはコンテナ内の `/app/config.json` に、音声は永続ボリュームの `/app/data` に置かれる。
このボリュームは、バイナリで直接実行した場合のホストの `./data` とは別の保存先。

```sh
docker compose build
docker compose run --rm radipod sync -config /app/config.json -dry-run
docker compose up -d
docker compose logs -f --tail=100
```

AntennaPodには `config.local.json` の `base_url` に `/feed.xml` を付けたURLを登録する。
GoとFFmpegはコンテナに含まれるので、ホストへのインストールは不要。
終了時は `docker compose down`、設定変更後は `docker compose restart`。通常のdownでは音声ボリュームは残る。
音声を残す場合は `docker compose down -v` を使わない。

必要なら `.env.example` を `.env` にコピーして公開ポート・待受IP・Basic認証を設定する。
環境変数や公開ポートを変更した場合は `docker compose up -d` でコンテナを再作成する。
公開ポートを変えた場合は `base_url` のポートも合わせる。

## 保存形式

```text
data/episodes/
  LFR_20261016010000_20261016030000/
    audio.m4a
    episode.json
```

メタデータには番組名、説明、放送局、開始・終了日時、実音声長、バイト数、保存日時を記録する。
IDとRSSのGUIDは放送局・開始・終了日時から決まり、配信アドレスが変わっても変わらない。
短い試し録りと番組全体の録音は別IDになる。

取得中は `.pending-*` に保存し、音声とメタデータが揃ってからディレクトリをrenameする。
エラーや通常の中断では一時ファイルを削除する。強制終了・電源断で残った `.pending-*` は配信されず、録音プロセスが停止していることを確認した後で削除できる。
自動削除・容量制限・途中からの再開は未実装。再実行時は未完了の番組を最初から取り直す。

## 開発・検証

```sh
make check
go test -race ./...
```

テストは実際のradikoへ接続しない。HTTPモック、保存失敗・重複、RSSのXML、音声Range、認証、日付境界を確認する。
FFmpegとffprobeがある環境では、合成音声のHLS取得・M4A保存も実行する。このテストにはループバックのポート待受が必要。

2026-10-17にmacOS/arm64・Go 1.26.5で確認した内容:

- `go test -race ./...` と `go vet ./...` が成功
- 実際のradikoで認証・接続元エリアの局一覧を取得
- RN1の10秒、および5分10秒（2チャンク）の音声をM4A保存
- RSSのenclosure URLから音声を取得し、バイト数・HTTP Rangeの206応答・FFmpegによるデコードを確認
- HTTPサーバーのSIGTERMによる終了を確認
- 設定ファイルの条件から実際の番組表を検索する `sync -dry-run` を確認（5件、音声の取得なし）
- 定期巡回の再試行・番組重複排除・認証更新・設定検証のテストを確認
- Linux amd64 / arm64向けのクロスビルドとCompose設定の構文検証を確認

Docker Engineが検証環境で停止しているため、Dockerイメージのビルドとコンテナ実行は未検証。

検証用の録音はリポジトリ外の一時フォルダーに保存し、リポジトリには含めていない。

| 場所 | 役割 |
| --- | --- |
| `cmd/radipod` | CLI・番組選択・処理の組み立て |
| `internal/app` | 共通の取得処理・定期巡回・再取得 |
| `internal/config` | JSON設定の読込・検証 |
| `internal/radiko` | 認証・番組情報・配信URL |
| `internal/recorder` | FFmpeg実行・再試行・音声長の検証 |
| `internal/store` | 音声・メタデータの保存 |
| `internal/feed` | RSS生成 |
| `internal/server` | RSS・音声のHTTP配信 |

認証トークンはメモリ上だけで扱う。録音処理からFFmpegはシェルを経由せず呼び出す。
AntennaPod実機での購読・ダウンロード・再生は、スマホと自宅サーバーを接続して確認する必要がある。

## 参考

- [AntennaPod](https://antennapod.org/)
- [AntennaPodの自動ダウンロード](https://antennapod.org/ja/documentation/automation/automatic-downloads)
- [FFmpeg](https://ffmpeg.org/)
- [プロトコルの参考実装とライセンス](THIRD_PARTY_NOTICES.md)
