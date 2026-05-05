# FinTrack — Statement Parser

Python (FastAPI) microservice that parses Mongolian bank statements
(PDF / Excel / CSV) into a structured JSON. Used by the Go backend's
`/api/v1/ai/analysis` endpoint.

## Endpoints

### `POST /parse`
multipart/form-data: `file=<bank statement>`

Response: see `ParsedStatement` model in `main.py`. Includes:

- `bank_name`
- `opening_balance`, `closing_balance`
- `total_income`, `total_expenses`
- `period_start`, `period_end`
- `transactions[]` — each with `date`, `description`, `amount`, `type`,
  `category`, `balance`
- `category_breakdown[]` — top expense categories with percentage

### `GET /health`
Healthcheck used by docker-compose.

## Run locally

```bash
cd parser_service
pip install -r requirements.txt
uvicorn main:app --reload --port 8000
```

## Run via docker-compose

```bash
docker-compose up parser
```

The Go backend reads `PARSER_SERVICE_URL` from env. In docker-compose this
is set to `http://parser:8000`. If the service is unreachable the Go
backend falls back to its built-in (lower quality) parser.
