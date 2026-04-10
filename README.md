# ✍️ AP Voice

[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/ap-voice)](https://golang.org/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/ap-voice)](https://github.com/shouni/ap-voice/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## 💡 概要 (About)— **堅牢なGo並列処理とAIを統合した次世代ドキュメント音声化パイプライン**

**AP Voice** は、独自の **Gemini API クライアントライブラリ** [`shouni/go-gemini-client`](https://github.com/shouni/go-gemini-client) と **Go言語の強力な並列制御**を融合させたCLI ツールです。

長文の技術ドキュメントやWeb記事を、AIが話者とスタイルを明確に指示した**ナレーションスクリプト**に変換するだけでなく、その台本をローカルの **VOICEVOXエンジンに高速接続**し、**最終的な音声ファイル (WAV)** を生成します。

本ツールは **Google Cloud 連携に最適化された I/O 設計**を採用。入力ソースとして **Web URL**、**GCS (`gs://`)** を透過的に扱うことができ、生成された音声も**ローカルまたは GCS** へ直接保存可能です。

## ✨ 主な特徴 (Features)

* **✍️ AI-Driven Scripting**:
    * AIが技術ドキュメントを解析し、最適な話者スタイルを指定したナレーションスクリプトを自動生成。
* **🔗 Cloud Native Input**:
    * Web URL、ローカルファイル、GCS (`gs://`) からの直接読み込みを標準サポート。
* **⚡️ High-Speed Parallel Synthesis**:
    * Go言語の並列処理と堅牢なリトライロジックを融合。VOICEVOXエンジンへの高速接続により、長文の音声合成も高い安定性と成功率で完結。
* **🧬 Unified Audio Pipeline**:
    * スクリプト生成からWAV出力、ストレージ保存までを一貫したCLIで完結。複数ツールの連携作業を自動化し、ストレスフリーなドキュメント配信を実現。

---

## ✨ 技術スタック (Technology Stack)

| 要素 | 技術 / ライブラリ | 役割 |
| --- | --- | --- |
| **言語** | **Go (Golang)** | クロスプラットフォームでの高速な実行を実現。 |
| **並行処理** | **Goroutines (`sync` パッケージ)** | 複数の音声リクエストを**並列**で実行し、処理時間を大幅に短縮。 |
| **AI モデル** | **Google Gemini API** | 独自のクライアントを通じ、入力文章を高度に分析・スクリプト化。 |
| **リモート I/O** | **`shouni/go-remote-io`** | **GCS, ローカル**への透過的な読み書き（Input/Output）を管理。 |
| **コンテンツ抽出** | **`shouni/go-web-exact`** | URLから不要な広告要素を排除し、本文のみを特定して抽出。 |
| **ネットワーク** | **`shouni/go-http-kit`** | 通信におけるリトライとタイムアウトを堅牢に制御。 |

---

## 🛡️ 入出力とVOICEVOX連携の堅牢性

### 1. 透過的な I/O 管理 (Unified I/O)

* **コンテンツ抽出**: `--url (-u)` を指定すると、Webサイトを解析し、タイトルと本文のみをクリーンに取得します。また、パスの接頭辞 (**`gs://`**) を自動判別し、クラウド上のドキュメントを直接ストリームとして読み込むことも可能です。
* **一貫した出力**: 音声ファイル（WAV）の出力先も同様に、ローカルおよび **GCS (`gs://`)** をシームレスに切り替えます。

### 2. 超高速な並列処理 (High-Speed Parallel Processing)

スクリプトを文単位のセグメントに分割し、**GoのGoroutine**を用いて並列に音声合成を行います。最大並列実行数を制御することでエンジンの過負荷を防ぎつつ、逐次処理と比較して合成時間を劇的に短縮します。

### 3. スタイルIDの自動フォールバック

AIが生成したスタイルタグが VOICEVOX 側で定義されていない場合、自動的にその話者の「ノーマル」スタイルにフォールバックします。これにより、AIの微細な表現のゆらぎによってパイプラインが停止することはありません。

---

## ✨ 主な機能

1. **Webからの自動抽出**: URLから記事タイトルと本文のみを整形してAIに渡します。
2. **マルチプロトコル入力**: ローカル、**GCS (`gs://`)** に対応。
3. **AIスクリプト生成**: **`solo`**, **`dialogue`**, **`duet`** の3形式をサポート。
4. **VOICEVOX並列合成**: 生成された台本を並列処理で高速にWAV化し、連結して出力。
5. **クラウド直接出力**: 生成されたWAVを **GCS (`gs://`)** へ直接保存可能。

---

## 📦 使い方

### 1. 環境設定

| 変数名 | 必須/任意 | 説明 |
| --- | --- | --- |
| `GEMINI_API_KEY` | 必須 | Google AI Studio で取得した API キー。 |
| `VOICEVOX_API_URL` | VOICEVOX使用時 | エンジンのURL (例: `http://localhost:50021`)。 |
| `GOOGLE_APPLICATION_CREDENTIALS` | GCS使用時 | GCS権限を持つサービスアカウントのJSONパス。 |

### 2. スクリプト生成コマンド

```bash
paidgo generate [flags]

```

#### フラグ一覧（入力ソースはいずれか一つを指定）

| フラグ | 短縮形 | 説明 |
| --- | --- | --- |
| `--url` | `-u` | **入力ソースURI**。Web URL、GCS (`gs://`)を指定します。 |
| `--output` | `-o` | 生成スクリプト（テキスト）の保存先。省略時は標準出力。 |
| `--mode` | `-m` | 形式: **`solo`**, **`dialogue`**, **`duet`** (Default: `duet`)。 |
| `--voicevox` | `-v` | 音声WAVの保存先。ローカルパスまたは **`gs://`** (GCS)。 |
| `--http-timeout` |  | Webリクエストや合成のタイムアウト時間。 (Default: `60s`) |

---

## 🔊 実行例

### 例 1: Web記事を対話形式で音声化し、GCSへ保存

```bash
# Webから入力し、生成された音声をGCSへ直接アップロード
./bin/paidgo generate \
    --url "https://example.com/tech-news" \
    --mode dialogue \
    --voicevox "gs://my-audio-bucket/output/news.wav"

```

### 例 2: GCS上の文書を読み込み、モノローグ化してローカルに保存

```bash
./bin/paidgo generate \
    --url "gs://my-source-bucket/docs/article.md" \
    --mode solo \
    --voicevox "article.wav"

```

---

## 🤝 依存関係 (Dependencies)

* [shouni/go-gemini-client](https://github.com/shouni/go-gemini-client) - Gemini API 通信の抽象化と生成ロジックの最適化
* [shouni/go-voicevox](https://github.com/shouni/go-voicevox) - VOICEVOX エンジンとの通信および音声合成の制御
* [shouni/go-remote-io](https://github.com/shouni/go-remote-io) - ストレージを透過的に扱うマルチストレージ I/O

---

### 📜 ライセンス (License)

* デフォルトキャラクター: VOICEVOX:ずんだもん、VOICEVOX:四国めたん
* このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
