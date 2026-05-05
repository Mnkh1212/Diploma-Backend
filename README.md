# FinTrack — AI-Powered Personal Finance Manager

Хиймэл оюун ухаанд суурилсан хувийн санхүүгийн хяналт, зөвлөмжийн систем.

Diploma project — AI-based personal finance tracking and recommendation system.

## Tech Stack

| Layer     | Technology                                      |
| --------- | ----------------------------------------------- |
| Mobile    | React Native (Expo SDK 55) + TypeScript + NativeWind v4 |
| Web       | React 18 + Vite + TypeScript + Tailwind + Recharts |
| Backend   | Go 1.23 (Gin framework + GORM)                  |
| Parser    | Python 3.12 (FastAPI + pdfplumber + pandas)     |
| Database  | PostgreSQL 16                                    |
| AI        | Gemini (chat + structured statement analysis)    |
| Infra     | Docker + Docker Compose                          |
| Deployment | Physical iPhone via USB (expo-dev-client) / Web (Vite preview) |

## Project Structure

```
.
├── backend/                  # Go backend API
│   ├── cmd/server/           # Entry point (main.go)
│   ├── internal/
│   │   ├── config/           # Environment config
│   │   ├── database/         # DB connection, migrations, seed
│   │   ├── handlers/         # HTTP handlers (8 files)
│   │   │   ├── auth.go
│   │   │   ├── transaction.go
│   │   │   ├── dashboard.go
│   │   │   ├── budget.go
│   │   │   ├── account.go
│   │   │   ├── ai_chat.go
│   │   │   ├── scheduled_payment.go
│   │   │   └── activity_log.go
│   │   ├── middleware/       # JWT auth, CORS middleware
│   │   ├── models/           # Data models & DTOs (9 models)
│   │   └── routes/           # API route definitions
│   ├── Dockerfile            # Multi-stage Go build
│   └── go.mod
├── frontend/                 # React Native (Expo) app
│   ├── src/
│   │   ├── screens/          # 14 app screens (.tsx)
│   │   ├── navigation/       # Stack + Bottom Tab navigators
│   │   ├── services/         # Axios API service (typed)
│   │   ├── context/          # AuthContext (login, register, logout)
│   │   └── types/            # All TypeScript types & interfaces
│   ├── tailwind.config.js    # Dark theme design tokens
│   ├── babel.config.js       # NativeWind v4 config
│   ├── metro.config.js       # Metro + NativeWind
│   └── app.json
├── docs/                     # Documentation
│   └── work.md               # Work log
├── docker-compose.yml        # PostgreSQL + Backend orchestration
└── README.md
```

## Getting Started

### Prerequisites

- Docker & Docker Compose
- Node.js 18+
- Go 1.23+ (Mac: `brew install go`)
- Xcode (iOS development)
- Physical iPhone + USB cable

### 1. Start Backend (Docker)

```bash
# Start PostgreSQL + Go backend
docker-compose up --build -d

# Backend runs on http://localhost:8080
# PostgreSQL on port 5433 (mapped from 5432)
```

AI chat-ийн жинхэнэ model ашиглахын тулд `backend/.env.example`-ийг жишээ болгож `AI_API_KEY`, `GEMINI_API_KEY`, эсвэл `GOOGLE_API_KEY`-ийн аль нэгийг тохируулна.

### 2. Connect Database (DataGrip)

| Field    | Value       |
|----------|-------------|
| Host     | localhost   |
| Port     | 5433        |
| User     | fintrack    |
| Password | fintrack123 |
| Database | fintrack    |

### 3. Start Frontend

```bash
cd frontend
npm install

# For physical iPhone (USB cable):
npx expo run:ios --device

# For development:
npx expo start --clear --dev-client
```

API host-ийг нэг газраас удирдах бол `frontend/src/config/network.ts`-д өөрчилнө.

### 4. iPhone Setup

1. Xcode → Settings → Accounts → Add Apple ID
2. Select project → Signing & Capabilities → Team: Personal Team
3. iPhone → Settings → General → VPN & Device Management → Trust developer

## API Endpoints (38+ routes)

### Auth (Public)
- `POST /api/v1/auth/register` — Бүртгүүлэх
- `POST /api/v1/auth/login` — Нэвтрэх

### Profile
- `GET /api/v1/profile` — Профайл харах
- `PUT /api/v1/profile` — Профайл шинэчлэх

### Dashboard & Analytics
- `GET /api/v1/dashboard` — Нүүр хуудасны мэдээлэл
- `GET /api/v1/expenses/summary` — Зарлагын нэгтгэл (period query)
- `GET /api/v1/statistics` — Статистик (period query)

### Transactions
- `GET /api/v1/transactions` — Гүйлгээний жагсаалт (paginated, filterable)
- `GET /api/v1/transactions/:id` — Нэг гүйлгээ
- `POST /api/v1/transactions` — Шинэ гүйлгээ
- `DELETE /api/v1/transactions/:id` — Гүйлгээ устгах

### Budgets
- `GET /api/v1/budgets` — Төсвийн жагсаалт
- `POST /api/v1/budgets` — Шинэ төсөв
- `PUT /api/v1/budgets/:id` — Төсөв шинэчлэх
- `DELETE /api/v1/budgets/:id` — Төсөв устгах

### Accounts
- `GET /api/v1/accounts` — Дансны жагсаалт
- `POST /api/v1/accounts` — Шинэ данс
- `PUT /api/v1/accounts/:id` — Данс шинэчлэх
- `DELETE /api/v1/accounts/:id` — Данс устгах

### Categories
- `GET /api/v1/categories` — Ангилал (type filter)

### Scheduled Payments
- `GET /api/v1/scheduled-payments` — Давтагдах төлбөрүүд
- `POST /api/v1/scheduled-payments` — Шинэ давтагдах төлбөр
- `DELETE /api/v1/scheduled-payments/:id` — Устгах
- `PUT /api/v1/scheduled-payments/:id/toggle` — Идэвхжүүлэх/зогсоох

### AI Chat
- `POST /api/v1/ai/chats` — Шинэ чат үүсгэх
- `GET /api/v1/ai/chats` — Чатын жагсаалт
- `GET /api/v1/ai/chats/:id` — Чат мессежүүдтэй
- `POST /api/v1/ai/chat` — Мессеж илгээх (AI хариу)
- `DELETE /api/v1/ai/chats/:id` — Чат устгах

### AI Analysis (банкны хуулга → structured JSON)
- `POST /api/v1/ai/analysis` — Хуулга оруулж AI шинжилгээ хийх (multipart/form-data)
- `GET /api/v1/ai/analyses` — Өмнөх анализуудын жагсаалт
- `GET /api/v1/ai/analyses/:id` — Нэг анализ
- `DELETE /api/v1/ai/analyses/:id` — Устгах

`POST /api/v1/ai/analysis` нь дараах structured JSON-ыг буцаана: `opening_balance`,
`closing_balance`, `total_income`, `total_expenses`, `net_cashflow`,
`transactions[]`, `categories[]` (amount + percentage), `recommendations[]`,
`ai_summary`. Frontend нь чарт зурахад шууд ашиглана.

### Activity Logs
- `GET /api/v1/activity-logs` — Лог жагсаалт (paginated, filterable)
- `GET /api/v1/activity-logs/summary` — Лог нэгтгэл

## Database Models (11 tables)

| Model            | Description                            |
|------------------|----------------------------------------|
| User             | Хэрэглэгч (JWT auth, bcrypt password) |
| Account          | Данс (bank, cash, credit_card, investment) |
| Category         | Ангилал (17 seed categories)           |
| Transaction      | Орлого/Зарлага гүйлгээ                |
| Budget           | Сарын төсөв (category-based)           |
| ScheduledPayment | Давтагдах төлбөр                       |
| AIChat           | AI чатын session                       |
| AIMessage        | Чатын мессежүүд                        |
| ActivityLog      | Хэрэглэгчийн үйлдлийн бүртгэл        |
| Notification     | Push notification (insight/warning/tip) |
| AIAnalysis      | Банкны хуулга AI structured анализ      |

## Components

```
Diploma-Backend/
├── cmd/server/             # Go entry point
├── internal/               # Go domain code
├── parser_service/         # Python FastAPI parser microservice
└── docker-compose.yml      # postgres + parser + backend

Diploma-Frontend/           # React Native (Expo) — mobile
Diploma-Web/                # React (Vite + Tailwind) — web client
```

## Features

- **Dashboard**: Нийт баланс, орлого/зарлагын нэгтгэл, хадгаламжийн хувь
- **Transaction Tracking**: Гүйлгээ бүртгэх, хайх, шүүх (paginated)
- **Expense Analytics**: Зарлагын категори, хувь хэмжээ, donut chart
- **Budget Management**: Сарын төсөв тогтоох, хяналт, progress bar
- **Statistics**: Daily/Weekly/Monthly/Yearly статистик, bar chart
- **AI Financial Advisor**: Context-aware санхүүгийн зөвлөмж (balance, trends, categories)
- **Scheduled Payments**: Давтагдах төлбөр бүртгэх, удирдах
- **Account Management**: Олон данс (bank, cash, credit card, investment)
- **Profile Management**: Нэр, email, валют тохируулах
- **Activity Logging**: Бүх үйлдлийн бүртгэл (audit trail)
- **Dark Theme UI**: Figma дизайн дагуу бүрэн dark theme
- **Physical Device**: iPhone дээр USB-ээр ажиллана (expo-dev-client)

## Screenshots

| Screen      | Description          |
|-------------|----------------------|
| Onboarding  | Welcome + Get Started |
| Login       | Email + Password      |
| Register    | Name + Email + Password |
| Home        | Balance, accounts, quick actions, transactions |
| Analytics   | Scheduled payments, income/expenses, bar chart |
| Add (+)     | Amount, category, account selection |
| AI Chat     | Financial advisor conversation |
| Settings    | Profile, accounts, preferences |
| Transactions | Full history, search, tabs |
| Expenses    | Category breakdown, donut chart |
| Budget      | Monthly budget, progress bars |
| Profile     | Edit name, email, currency |
| Accounts    | Manage bank accounts |
| Recurring   | Scheduled payment management |

## Config Note

- Frontend API/server host нь `frontend/src/config/network.ts` дээр төвлөрсөн.
- Backend default host/port утгууд `backend/internal/config/defaults.go` дээр төвлөрсөн.
- Social auth scaffold нь `GOOGLE_IOS_CLIENT_ID`, `GOOGLE_WEB_CLIENT_ID`, `FACEBOOK_APP_ID`, `APPLE_BUNDLE_ID` env-үүдийг ашигладаг.
