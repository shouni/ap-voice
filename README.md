# ✍️ AP Voice

[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/ap-voice)](https://golang.org/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/ap-voice)](https://github.com/shouni/ap-voice/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Status](https://img.shields.io/badge/Status-Completed-brightgreen)](#)

## 💡 概要 (About)— **堅牢なGo並列処理とAIを統合した次世代ドキュメント音声化パイプライン**

**AP Voice** は、独自の **Gemini API クライアントライブラリ** [`shouni/go-gemini-client`](https://github.com/shouni/go-gemini-client) と **Go言語の強力な並列制御**を融合させた、Cloud Run + Cloud Tasks 上で動くサービスです。

長文の技術ドキュメントやWeb記事を、AIが話者とスタイルを明確に指示した**ナレーションスクリプト**に変換し、その台本を **VOICEVOXエンジンで合成**して**最終的な音声ファイル (WAV)** を生成します。

本ツールは **Google Cloud 連携に最適化された I/O 設計**を採用。入力ソースとして **Web URL**、**GCS (`gs://`)** を透過的に扱うことができ、生成された音声も**ローカルまたは GCS** へ直接保存可能です。

## ✨ 主な特徴 (Features)

* **✍️ AI-Driven Scripting**:
    * AIが技術ドキュメントを解析し、最適な話者スタイルを指定したナレーションスクリプトを自動生成。
* **🔗 Cloud Native Input**:
    * Web URL、GCS (`gs://`) からの直接読み込みをサポート。
* **⚡️ High-Speed Parallel Synthesis**:
    * Go言語の並列処理と堅牢なリトライロジックを融合。VOICEVOXエンジンへの高速接続により、長文の音声合成も高い安定性と成功率で完結。
* **🧬 Unified Audio Pipeline**:
    * スクリプト生成からWAV出力、ストレージ保存までを一貫したパイプラインで完結。1つのイメージを Web 面と Worker 面の2サービスとしてデプロイします。

---

## ✨ 技術スタック

| 要素 | 技術 / ライブラリ | 役割 |
| :--- | :--- | :--- |
| **言語** | **Go (Golang)** | ツールの開発言語。並列処理と堅牢な実行環境を提供します。 |
| **HTTP** | **chi** | ルーティングとミドルウェアに使用します。 |
| **実行基盤** | **Cloud Run + Cloud Tasks** | Web 面がタスクを投入し、Worker 面が実行します。 |

---

## ✨ 主な機能

1. **Webからの自動抽出**: URLから記事タイトルと本文のみを整形してAIに渡します。
2. **マルチソース入力**: Web URL、**GCS (`gs://`)** に対応。
3. **AIスクリプト生成**: **`solo`**, **`dialogue`**, **`duet`** の3形式をサポート。
4. **VOICEVOX並列合成**: 生成された台本を並列処理で高速にWAV化し、連結して出力。
5. **クラウド直接出力**: 生成されたWAVを **GCS (`gs://`)** へ直接保存可能。

---

## 📦 使い方

### 1. 環境設定

| 変数名 | 必須/任意 | 説明 |
| --- | --- | --- |
| `SERVER_ROLE` | 必須 | `web` / `worker` / `both` のいずれか（`both` はローカル開発用）。**未設定・未知の値は起動時エラー**です。担当する面だけを組み立て、ルートもその面のものだけを登録します。 |
| `TASK_AUDIENCE_URL` | worker で必須 | Cloud Tasks の OIDC 検証で使う audience（worker 自身の URL）。未設定なら `SERVICE_URL` を使います。 |
| `ALLOWED_TASK_SERVICE_ACCOUNTS` | worker で必須 | 受け付ける caller SA（カンマ区切り）。**投入側**の SA を指定します（web/worker で実行 SA を分けるため、worker には「他人の SA」が並びます）。 |
| `SERVICE_URL` | 任意 | サービスの公開 URL (Default: `http://localhost:8080`)。 |
| `PORT` | 任意 | 待ち受けポート (Default: `8080`)。 |
| `GEMINI_MODELS` | 必須 | 使用する Gemini モデル名。**カンマ区切りで複数指定でき、先頭が既定モデル**になります。`--model` / `-g` で上書きできます。**アプリ側に既定値は無く、未設定なら起動時にエラー**になります。 |
| `GCP_PROJECT_ID` | 必須 | GCP Project ID。**Gemini は Vertex AI 経由でのみ呼びます**（API キー経路は持ちません）。ローカル実行では ADC が必要です。 |
| `GCP_LOCATION_ID` | 任意 | Vertex AI のロケーション (Default: `global`)。 |
| `VOICEVOX_API_URL` | 任意 | エンジンのURL (例: `http://localhost:50021`)。未設定なら `http://localhost:50021` を使います。フラグはありません（実行ごとではなくデプロイ先が決める値のため）。 |
| `HTTP_TIMEOUT` | 任意 | 外部 HTTP 通信のタイムアウト (Default: `60s`)。 |
| `SLACK_WEBHOOK_URL` | 任意 | 完了・失敗の通知先。未設定なら通知は無効になります。 |
| `GOOGLE_APPLICATION_CREDENTIALS` | GCS使用時に必要な場合 | GCS権限を持つサービスアカウントのJSONパス（ADC利用時）。 |

> 環境変数が持つのは**デプロイ先が決める設定**だけです。入力元・出力先・生成モードといった
> 実行ごとに変わる値は、タスクのペイロード（JSON）で渡します。

### 2. 起動

```bash
go run .        # SERVER_ROLE が必須
```

`SERVER_ROLE` が担う面だけを組み立てます。

| ロール | 組み立てるもの | 公開されるルート |
| --- | --- | --- |
| `web` | （次のコミットで投入フォームを追加） | `GET /health` |
| `worker` | パイプライン（Gemini + VOICEVOX + GCS + 通知） | `GET /health`, `POST /tasks/generate` |
| `both` | 両方（ローカル開発用） | 上記すべて |

`POST /tasks/generate` は Cloud Tasks 専用で、OIDC 検証を通らないリクエストは 401 になります。
`SERVER_ROLE=web` のプロセスでは**ルートごと登録されない**ため 404 です。

#### タスクのペイロード

**台本生成と音声合成は別の入口です。** 台本は成果物であると同時に入力でもあり
（WAV の隣に `.json` で保存されます）、読みや話者を直して**合成だけやり直したい**ことが
普通に起こるためです。1つの入口しか無いと、そのたびに Gemini の生成からやり直すことになり、
費用と待ち時間が無駄になるうえ、直したかった1行以外まで別物になってしまいます。

| `command` | 何をするか | 必須フィールド |
| --- | --- | --- |
| `generate` | 入力ソースから台本を作り、そのまま音声まで作る | `input_uri`, `output_uri` |
| `synthesize` | **渡された台本から音声だけ作る**（Gemini を呼ばない） | `script`, `output_uri` |

| フィールド | 説明 |
| --- | --- |
| `command` | `generate` / `synthesize`。**省略できません**（`script` を渡したまま書き忘れると、台本が黙って捨てられて生成が走るため）。 |
| `input_uri` | **入力ソースURI**。Web URL、GCS (`gs://`)を指定します。`generate` で必須。 |
| `output_uri` | **出力先URI**。WAVを保存し、同名の `.json` スクリプトも保存します（例: `out.wav`, `gs://bucket/out.wav`）。 |
| `mode` | 形式: **`solo`**, **`dialogue`**, **`duet`**。`generate` のみ。 |
| `ai_model` | 使用する Gemini モデル名。空なら `GEMINI_MODELS` の先頭を使います。`generate` のみ。 |
| `script` | 台本（`ScriptLine` の配列）。`synthesize` で必須。保存された `.json` をそのまま貼り戻せます。 |

```json
{
  "command": "generate",
  "input_uri": "https://example.com/tech-news",
  "output_uri": "gs://my-bucket/audio/tech-news.wav",
  "mode": "dialogue"
}
```

```json
{
  "command": "synthesize",
  "output_uri": "gs://my-bucket/audio/tech-news.wav",
  "script": [
    { "speaker": "ずんだもん", "style": "ノーマル", "text": "直した台本なのだ" }
  ]
}
```

---

## 🔄 処理シーケンス図

```mermaid
sequenceDiagram
    autonumber
    participant Caller as 投入元
    participant Tasks as Cloud Tasks
    participant Worker as gcp-kit/worker Handler
    participant Notifier as domain.Notifier (Slack/Noop)
    participant Pipeline as pipeline.Pipeline
    participant GenRunner as runner.GenerateRunner
    participant Reader as go-web-reader
    participant Prompt as PromptAdapter
    participant Gemini as go-gemini-client (Gemini/Vertex AI)
    participant PubRunner as runner.PublishRunner
    participant Voice as go-voicevox
    participant Store as go-remote-io (Local/GCS)
    participant Signer as remoteio.URLSigner

    Caller->>Tasks: enqueue(domain.Request)
    Tasks->>Worker: POST /tasks/generate (OIDC)
    Note over Worker: 起動時に BuildContainer 済み

    Worker->>Pipeline: Execute(ctx, req)
    Pipeline->>GenRunner: Run(ctx, req)
    GenRunner->>Reader: Open(inputURI)
    Reader-->>GenRunner: source content
    GenRunner->>Prompt: Generate(mode, content)
    Prompt-->>GenRunner: prompt text
    GenRunner->>Gemini: GenerateContent(model, prompt)
    Gemini-->>GenRunner: script text
    GenRunner-->>Pipeline: script text

    Pipeline->>PubRunner: Run(ctx, outputURI, script)
    PubRunner->>Voice: UploadWav(outputURI, script)
    Voice->>Store: write wav (local/gs://)
    Store-->>PubRunner: ok
    PubRunner->>Voice: UploadScript(outputURI, script)
    Voice->>Store: write json (local/gs://)
    Store-->>PubRunner: ok
    opt signer is configured
        PubRunner->>Signer: GenerateSignedURL(outputURI, GET, 1h)
        Signer-->>PubRunner: publicURL
    end
    PubRunner-->>Pipeline: publicURL / ""
    Pipeline->>Notifier: Notify(req, publicURL)
    Notifier-->>Pipeline: ok
    Pipeline-->>Worker: ok
    Worker-->>Tasks: 2xx
```

## 🌳 プロジェクト構成ツリー図

```text
ap-voice/
├── main.go                  # エントリポイント（サーバー起動）
├── assets/                  # 埋め込みプロンプト管理（prompt_*.md）
└── internal/
    ├── config/              # 環境変数の読み込みと検証（SERVER_ROLE を含む）
    ├── server/              # chi ルーター・グレースフルシャットダウン
    ├── domain/              # ドメインモデルとポート定義
    ├── app/                 # DI コンテナとリソース管理
    ├── builder/             # 外部依存とパイプライン組み立て
    ├── pipeline/            # Generate/Publish 実行オーケストレーション
    ├── runner/              # 生成処理・公開処理のユースケース実装
    └── adapters/            # Gemini / Prompt / VOICEVOX の実装アダプタ
```

## 🤝 依存関係 (Dependencies)

主要な direct dependency（`go.mod`）:

* **[go-chi/chi](https://github.com/go-chi/chi)**: HTTP ルーティング
* **[shouni/gcp-kit](https://github.com/shouni/gcp-kit)**: SERVER_ROLE の語彙・Cloud Tasks ハンドラ・OIDC 検証・Cloud Logging
* **[shouni/go-gemini-client](https://github.com/shouni/go-gemini-client)**: Gemini / Vertex AI への生成リクエスト
* **[shouni/go-voicevox](https://github.com/shouni/go-voicevox)**: VOICEVOX エンジンによる音声合成
* **[shouni/go-web-reader](https://github.com/shouni/go-web-reader)**: `https://` / `gs://` 入力の読み込み
* **[shouni/go-remote-io](https://github.com/shouni/go-remote-io)**: ローカル/GCS への書き込み抽象化
* **[shouni/go-http-kit](https://github.com/shouni/go-http-kit)**: HTTP クライアント（タイムアウト/リトライ）
* **[shouni/go-prompt-kit](https://github.com/shouni/go-prompt-kit)**: プロンプトテンプレートのロードとレンダリング
* **[caarlos0/env](https://github.com/caarlos0/env)**: 環境変数から設定構造体への読み込み
* **[shouni/go-utils](https://github.com/shouni/go-utils)**: ログなどのユーティリティ

実行時の外部依存:

* **Vertex AI**: スクリプト生成
* **VOICEVOX Engine** (`VOICEVOX_API_URL`): 音声合成
* **Google Cloud Storage**（任意）: `gs://` 入出力利用時


---

### 📜 ライセンス (License)

* デフォルトキャラクター: VOICEVOX:ずんだもん、VOICEVOX:四国めたん
* このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
