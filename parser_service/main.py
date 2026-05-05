"""FinTrack — Bank Statement Parser microservice.

FastAPI service that receives a bank statement file (PDF / Excel / CSV) and
returns a structured JSON describing the transactions, totals and category
breakdown. Mongolian bank formats (Khan, Golomt, TDB, Khas, State) are
recognised through keyword heuristics; unknown formats fall back to a generic
row scanner.

Endpoint:
    POST /parse  multipart form-data (file=...) -> ParsedStatement JSON

This service is purposefully stateless — the Go backend stores results.
"""

from __future__ import annotations

import io
import logging
import re
from collections import defaultdict
from dataclasses import dataclass, field
from datetime import datetime
from typing import Iterable, List, Optional, Tuple

import pandas as pd
import pdfplumber
from fastapi import FastAPI, File, HTTPException, UploadFile
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
log = logging.getLogger("parser")

app = FastAPI(title="FinTrack Statement Parser", version="1.0.0")


# ===================== Schema =====================


class ParsedTransaction(BaseModel):
    date: str = ""
    description: str = ""
    amount: float = 0.0
    type: str = "expense"
    category: str = "Бусад"
    balance: float = 0.0


class CategoryBreakdown(BaseModel):
    category: str
    amount: float
    percentage: float
    count: int


class ParsedStatement(BaseModel):
    bank_name: str = "Unknown Bank"
    opening_balance: float = 0.0
    closing_balance: float = 0.0
    total_income: float = 0.0
    total_expenses: float = 0.0
    period_start: str = ""
    period_end: str = ""
    transactions: List[ParsedTransaction] = []
    category_breakdown: List[CategoryBreakdown] = []


# ===================== Helpers =====================


CATEGORY_RULES: List[Tuple[str, List[str]]] = [
    ("Хоол", ["хоол", "ресторан", "кафе", "food", "restaurant", "kfc", "mcdonald", "pizza"]),
    ("Такси", ["такси", "uber", "bolt", "taxi"]),
    ("Тээвэр", ["шатахуун", "petrol", "gas", "shell", "petrovis", "magicnet"]),
    ("Дэлгүүр", ["emart", "номин", "nomin", "minii", "минии", "store", "shop", "дэлгүүр", "circle k"]),
    ("Эрүүл мэнд", ["эмнэлэг", "pharmacy", "эмийн сан", "hospital", "clinic"]),
    ("Орон сууц", ["түрээс", "rent", "орон сууц", "ус сүлжээ", "халаалт"]),
    ("Интернет", ["unitel", "mobicom", "skytel", "gmobile", "интернет", "internet"]),
    ("Цалин", ["цалин", "salary", "tsalin", "wage"]),
    ("Шилжүүлэг", ["шилжүүлэг", "transfer"]),
    ("Зугаа цэнгэл", ["netflix", "spotify", "youtube", "tiktok", "subscription", "кино"]),
    ("Боловсрол", ["сургууль", "school", "tuition", "education", "boloвсрол", "course"]),
    ("Хадгаламж", ["хадгаламж", "deposit", "savings"]),
    ("Зээл", ["зээл", "loan", "credit"]),
]


def classify(desc: str) -> str:
    low = (desc or "").lower()
    for cat, keywords in CATEGORY_RULES:
        if any(k in low for k in keywords):
            return cat
    return "Бусад"


BANK_KEYWORDS = [
    # Khan Bank: ХААН БАНК / KHAN BANK / XAAH БАНК (PDF text extraction-аас хамаарч)
    ("Khan Bank", ["khan bank", "khaan bank", "хаан банк", "xaah банк"]),
    ("Golomt Bank", ["golomt bank", "голомт банк", "голомт"]),
    # TDB — "tdb bank" эсвэл "trade and development" гэж тодорхой ярьсан үед л таних
    # (TDBM13361 гэх мэт transfer ID-аас зайлсхийнэ)
    ("TDB", ["tdb bank", "trade and development bank", "худалдаа хөгжлийн банк"]),
    ("Khas Bank", ["khas bank", "хас банк", "xacbank"]),
    ("State Bank", ["state bank", "төрийн банк"]),
    ("M Bank", ["m bank", "м банк"]),
    ("Capitron Bank", ["capitron bank", "капитрон банк"]),
    ("Arig Bank", ["arig bank", "ариг банк"]),
]


def detect_bank(text: str) -> str:
    low = text.lower()
    for name, keys in BANK_KEYWORDS:
        if any(k in low for k in keys):
            return name
    return "Unknown Bank"


# Жинхэнэ мөнгөн дүнг данс дугаараас (10+ оронтой) ялгахын тулд
# зөвхөн децимал цэгтэй тоог (1,234.00 / 5000.50) amount гэж үзнэ
AMOUNT_RE = re.compile(r"-?\(?\s*(?:\d{1,3}(?:[ ,']\d{3})+|\d{1,9})\.\d{1,2}\s*\)?")
# Loose: decimal-гүй жижиг тоог fallback болгон. 6-аас илүү оронтой бол data ID гэж үзэн алгасна.
AMOUNT_RE_LOOSE = re.compile(r"(?<!\d)-?\d{1,6}(?:[ ,']\d{3})?(?!\d)")
DATE_RE = re.compile(
    r"(\d{4}[-/.]\d{1,2}[-/.]\d{1,2}|\d{1,2}[-/.]\d{1,2}[-/.]\d{2,4})"
)


def normalize_amount(raw: str) -> Optional[float]:
    if not raw:
        return None
    s = raw.strip()
    neg = False
    if s.startswith("(") and s.endswith(")"):
        neg = True
        s = s[1:-1]
    if s.startswith("-"):
        neg = True
        s = s[1:]
    s = s.replace(" ", "").replace(" ", "").replace("'", "").replace("₮", "").replace("MNT", "")
    if s.count(",") and s.count("."):
        # 1,234.56 → 1234.56
        s = s.replace(",", "")
    elif s.count(",") and not s.count("."):
        # 1,234 → 1234 (mongolian thousand separator)
        # эсвэл 12,34 → 12.34 (decimal)
        if len(s.split(",")[-1]) == 2:
            s = s.replace(",", ".")
        else:
            s = s.replace(",", "")
    try:
        v = float(s)
    except ValueError:
        return None
    return -v if neg else v


def normalize_date(raw: str) -> str:
    if not raw:
        return ""
    raw = raw.strip()
    # Try ISO and DMY
    for fmt in ("%Y-%m-%d", "%Y/%m/%d", "%Y.%m.%d", "%d-%m-%Y", "%d/%m/%Y", "%d.%m.%Y", "%d-%m-%y", "%d/%m/%y"):
        try:
            return datetime.strptime(raw, fmt).strftime("%Y-%m-%d")
        except ValueError:
            continue
    return raw


# ===================== Parsers =====================


def parse_pdf(content: bytes) -> Tuple[str, List[ParsedTransaction]]:
    """Extract text + transactions from a PDF bank statement."""
    text_parts: List[str] = []
    with pdfplumber.open(io.BytesIO(content)) as pdf:
        for page in pdf.pages:
            txt = page.extract_text() or ""
            text_parts.append(txt)
    text = "\n".join(text_parts)

    upper = text.upper()
    low = text.lower()

    # Khan Bank format-ыг түрүүлж шалгана. Хаан банк нь хүснэгт format-тай
    # (Эхний үлдэгдэл | Дебит | Кредит | Эцсийн үлдэгдэл) — ОРЛОГО/ЗАРЛАГА
    # keyword-гүй учир дараах Mongolian parser-аар уншиж чадахгүй.
    is_khan = (
        "хаан банк" in low
        or "khan bank" in low
        or "khaan bank" in low
        or ("эхний үлдэгдэл" in low and "дебит" in low and "кредит" in low)
    )
    if is_khan:
        khan_txs = parse_khan_format(content, text)
        if khan_txs:
            return text, khan_txs

    # Mongolian bank format-ыг шалгана. Хэрэв "ОРЛОГО" / "ЗАРЛАГА" cyrillic
    # keyword-ууд ихтэй бол Mongolian parser ашиглана — ингэснээр илүү нарийн.
    if upper.count("ОРЛОГО") + upper.count("ЗАРЛАГА") >= 4:
        txs = parse_mongolian_format(text)
        if txs:
            return text, txs

    # Generic table extraction
    txs: List[ParsedTransaction] = []
    with pdfplumber.open(io.BytesIO(content)) as pdf:
        for page in pdf.pages:
            for table in page.extract_tables() or []:
                if not table:
                    continue
                header = [str(c or "").strip().lower() for c in table[0]]
                for row in table[1:]:
                    tx = row_to_tx(header, row)
                    if tx:
                        txs.append(tx)

    # Last resort: line-based fallback
    if not txs:
        for line in text.splitlines():
            tx = line_to_tx(line)
            if tx:
                txs.append(tx)
    return text, txs


# Хаан банкны хуулга нь хүснэгт format. Багануудын тоо PDF-ээс хамаарч өөрчлөгдөнө,
# тиймээс header-ийн нэрээр баганыг олж унших нь хамгийн найдвартай.
KHAN_HEADER_HINTS = {
    "date": ["огноо"],
    "branch": ["салбар"],
    "open": ["эхний үлдэгдэл"],
    "debit": ["дебит"],
    "credit": ["кредит"],
    "close": ["эцсийн үлдэгдэл"],
    "desc": ["гүйлгээний утга", "утга"],
    "account": ["харьцсан данс", "данс"],
}


def _khan_col(header: List[str], hints: List[str]) -> Optional[int]:
    for idx, h in enumerate(header):
        for hint in hints:
            if hint in h:
                return idx
    return None


def parse_khan_format(content: bytes, text: str) -> List[ParsedTransaction]:
    """Хаан банкны хуулгад зориулсан parser.

    PDF-ийн хүснэгтийг pdfplumber-ийн extract_tables-аар уншиж, Дебит / Кредит
    баганыг тус тусад нь шалгана. Дебит > 0 → expense, Кредит > 0 → income.
    Хэрэв хоёулаа хоосон бол эцсийн – эхний үлдэгдлийн зөрүүгээр шийднэ.
    """
    txs: List[ParsedTransaction] = []
    seen_keys: set = set()

    try:
        with pdfplumber.open(io.BytesIO(content)) as pdf:
            for page in pdf.pages:
                for table in page.extract_tables() or []:
                    if not table or len(table) < 2:
                        continue
                    header = [str(c or "").strip().lower() for c in table[0]]
                    if not any("эхний үлдэгдэл" in h for h in header):
                        continue

                    col_date = _khan_col(header, KHAN_HEADER_HINTS["date"])
                    col_open = _khan_col(header, KHAN_HEADER_HINTS["open"])
                    col_debit = _khan_col(header, KHAN_HEADER_HINTS["debit"])
                    col_credit = _khan_col(header, KHAN_HEADER_HINTS["credit"])
                    col_close = _khan_col(header, KHAN_HEADER_HINTS["close"])
                    col_desc = _khan_col(header, KHAN_HEADER_HINTS["desc"])
                    col_account = _khan_col(header, KHAN_HEADER_HINTS["account"])

                    if col_open is None or col_close is None:
                        continue

                    for row in table[1:]:
                        if not row:
                            continue

                        def cell(i: Optional[int]) -> str:
                            if i is None or i >= len(row):
                                return ""
                            return str(row[i] or "").strip()

                        date_raw = cell(col_date)
                        # Огноогүй нэгтгэлийн мөрнүүд (нийт дүн г.м.)-ийг алгасна
                        if not re.search(r"\d{4}[-/.]\d{1,2}[-/.]\d{1,2}", date_raw):
                            continue

                        open_bal = normalize_amount(cell(col_open)) or 0.0
                        debit_raw = cell(col_debit) if col_debit is not None else ""
                        credit_raw = cell(col_credit) if col_credit is not None else ""
                        close_bal = normalize_amount(cell(col_close)) or 0.0

                        debit = abs(normalize_amount(debit_raw) or 0.0) if debit_raw else 0.0
                        credit = abs(normalize_amount(credit_raw) or 0.0) if credit_raw else 0.0

                        amount = 0.0
                        tx_type = "expense"
                        if credit > MIN_TX_AMOUNT:
                            amount = credit
                            tx_type = "income"
                        elif debit > MIN_TX_AMOUNT:
                            amount = debit
                            tx_type = "expense"
                        else:
                            diff = close_bal - open_bal
                            if abs(diff) < MIN_TX_AMOUNT:
                                continue
                            amount = abs(diff)
                            tx_type = "income" if diff > 0 else "expense"

                        desc = cell(col_desc) or "—"
                        account = cell(col_account) if col_account is not None else ""
                        if account and account not in desc:
                            desc = f"{desc} ({account})"
                        desc = re.sub(r"\s+", " ", desc).strip()[:200]

                        date = normalize_date(re.search(r"\d{4}[-/.]\d{1,2}[-/.]\d{1,2}", date_raw).group(0))
                        key = (date, amount, tx_type, desc[:60])
                        if key in seen_keys:
                            continue
                        seen_keys.add(key)

                        txs.append(ParsedTransaction(
                            date=date,
                            description=desc,
                            amount=amount,
                            type=tx_type,
                            category=classify(desc),
                            balance=close_bal,
                        ))
    except Exception as exc:  # noqa: BLE001
        log.warning("khan table parse failed: %s", exc)

    if txs:
        return txs

    # Fallback: text-based row matching. Хаан банкны хуулгад мөр тус бүрд:
    # № YYYY/MM/DD HH:MM 5XXX <opening> [<debit_or_credit>] <closing> <desc>
    # 4 оронтой салбарын код (5000, 5008, 5021, 5031, 5076 г.м) нь анкер.
    return _parse_khan_text(text)


KHAN_ROW_RE = re.compile(
    r"^\s*\d+\s+(\d{4}/\d{2}/\d{2})\s+\d{1,2}:\d{2}\s+(\d{4})\s+(.+)$"
)


def _parse_khan_text(text: str) -> List[ParsedTransaction]:
    txs: List[ParsedTransaction] = []
    seen_keys: set = set()
    for raw_line in text.splitlines():
        m = KHAN_ROW_RE.match(raw_line)
        if not m:
            continue
        date = normalize_date(m.group(1))
        rest = m.group(3)
        amounts = AMOUNT_RE.findall(rest)
        if len(amounts) < 2:
            continue
        # 2 эсвэл 3 дүн байж болно. Хамгийн утга учиртай нь:
        #   - 3 байвал: open, debit_or_credit, close (debit нь "-" prefix-тэй байдаг)
        #   - 2 байвал: open, close (no movement) — энэ үед алгасна
        nums = [normalize_amount(a) for a in amounts[:3]]
        nums = [n for n in nums if n is not None]
        if len(nums) < 2:
            continue
        if len(nums) >= 3:
            open_bal = nums[0]
            mid = nums[1]
            close_bal = nums[2]
            # Дэлэгрэнгүй: open ба close-ийн зөрүү нь mid-ийн абсолют утгатай ойролцоо байх ёстой
            diff = close_bal - open_bal
            amount = abs(mid)
            tx_type = "income" if diff > 0 else "expense"
            # Хэрэв зөрүү бараг 0 бол mid-ийн тэмдгээр тодорхойлно
            if abs(diff) < 1:
                continue
        else:
            open_bal = nums[0]
            close_bal = nums[1]
            diff = close_bal - open_bal
            if abs(diff) < MIN_TX_AMOUNT:
                continue
            amount = abs(diff)
            tx_type = "income" if diff > 0 else "expense"

        if amount < MIN_TX_AMOUNT:
            continue

        # Тайлбар — сүүлийн тооны араас
        desc = rest
        for a in amounts[:3]:
            desc = desc.replace(a, "", 1)
        desc = re.sub(r"\s+", " ", desc).strip(" -|") or "—"

        key = (date, amount, tx_type, desc[:60])
        if key in seen_keys:
            continue
        seen_keys.add(key)

        txs.append(ParsedTransaction(
            date=date,
            description=desc[:200],
            amount=amount,
            type=tx_type,
            category=classify(desc),
            balance=close_bal,
        ))
    return txs


# Монгол банкны хуулга-д тааруулсан parser. Голомт зэрэг банкны formats:
#
#     2026-01-24
#     50,000.00   ОРЛОГО     ШИЛЖҮҮЛЭГ-МӨНХ-ЭРДЭНЭ ...
#         500.00   ЗАРЛАГА    Данс хөтөлсний шимтгэл ...
#     50,000.00            ӨДРИЙН ОРЛОГО         <- алгасна
#        500.00            ӨДРИЙН ЗАРЛАГА        <- алгасна
#     54,905.50            ӨДРИЙН ҮЛДЭГДЭЛ      <- алгасна
#
# Дүн нь keyword-ээс өмнө байх онцлогтой.

DAILY_SKIP_KEYWORDS = (
    "ӨДРИЙН ОРЛОГО",
    "ӨДРИЙН ЗАРЛАГА",
    "ӨДРИЙН ҮЛДЭГДЭЛ",
)

DATE_LINE_RE = re.compile(r"^\s*(\d{4}[-/.]\d{1,2}[-/.]\d{1,2}|\d{1,2}[-/.]\d{1,2}[-/.]\d{2,4})\s*$")


def parse_mongolian_format(text: str) -> List[ParsedTransaction]:
    txs: List[ParsedTransaction] = []
    current_date = ""
    pending: Optional[ParsedTransaction] = None  # multi-line description-ыг үргэлжлүүлэхэд

    for raw_line in text.splitlines():
        line = raw_line.strip()
        if not line:
            pending = None
            continue

        # Огнооны мөр (зөвхөн огноо ганцаараа)
        m = DATE_LINE_RE.match(line)
        if m:
            current_date = normalize_date(m.group(1))
            pending = None
            continue

        upper = line.upper()

        # Өдрийн нэгтгэлийн мөр — алгасна
        if any(sk in upper for sk in DAILY_SKIP_KEYWORDS):
            pending = None
            continue

        # Нийт болон эхний/эцсийн үлдэгдлийн мөр (parse_statement дотор тус
        # extract_summary_amounts-аар авна) — энд алгасна.
        if (
            "НИЙТ ОРЛОГО" in upper
            or "НИЙТ ЗАРЛАГА" in upper
            or "ЭХНИЙ ҮЛДЭГДЭЛ" in upper
            or "ЭЦСИЙН ҮЛДЭГДЭЛ" in upper
        ):
            pending = None
            continue

        # Гүйлгээний мөр — keyword "ОРЛОГО" эсвэл "ЗАРЛАГА" агуулна
        tx_type: Optional[str] = None
        keyword: Optional[str] = None
        # ЗАРЛАГА-г эхэнд шалгах (income/expense зөв ялгахын тулд)
        if "ЗАРЛАГА" in upper:
            tx_type = "expense"
            keyword = "ЗАРЛАГА"
        elif "ОРЛОГО" in upper:
            tx_type = "income"
            keyword = "ОРЛОГО"

        if tx_type and keyword:
            kw_idx = upper.index(keyword)
            before = line[:kw_idx]
            after = line[kw_idx + len(keyword):].strip()

            # before хэсгээс хамгийн сүүлийн (хамгийн том) тоог дүн гэж авна
            amounts = AMOUNT_RE.findall(before)
            picked: Optional[float] = None
            for a in reversed(amounts):
                v = normalize_amount(a)
                if v is not None and abs(v) >= 100:
                    picked = abs(v)
                    break
            if picked is None:
                pending = None
                continue

            tx = ParsedTransaction(
                date=current_date,
                description=after if after else "—",
                amount=picked,
                type=tx_type,
                category=classify(after),
            )
            txs.append(tx)
            pending = tx
            continue

        # Үргэлжилсэн тайлбарын мөр (амжилт нь сөрөг ёсоор):
        # Хэрэв өмнөх pending tx байгаа бөгөөд энэ мөр нь "ОРЛОГО"/"ЗАРЛАГА"
        # биш бол tx-ийн description-д залгана.
        if pending and not _looks_like_data_row(line):
            pending.description = (pending.description + " " + line).strip()
            pending.category = classify(pending.description)

    return txs


def _looks_like_data_row(line: str) -> bool:
    """Тоогоор эхэлдэг мөр уу? (өөр гүйлгээ эхэлж магадгүй)"""
    s = line.strip()
    if not s:
        return False
    return bool(re.match(r"^[\d\s,'.]+$", s.split()[0]))


# Banks summary line хайх. "НИЙТ ОРЛОГО: 4,662,900.00" гэх мэт.
SUMMARY_PATTERNS: List[Tuple[str, List[str]]] = [
    ("total_income", [
        r"нийт\s*орлого",
        r"total\s*income",
        r"total\s*credit",
        r"нийт\s*кредит",
        r"нийт\s*кредит\s*гүйлгээ",
    ]),
    ("total_expense", [
        r"нийт\s*зарлага",
        r"total\s*expense",
        r"total\s*debit",
        r"нийт\s*дебет",
        r"нийт\s*дебит",
        r"нийт\s*дебит\s*гүйлгээ",
    ]),
    ("opening_balance", [
        r"эхний\s*үлдэгдэл",
        r"opening\s*balance",
        r"тайлант\s*үеийн\s*эхний",
        r"эхний\s*\(.*?\)\s*үлдэгдэл",
        r"эхний\s*дансны\s*үлдэгдэл",
    ]),
    ("closing_balance", [
        r"эцсийн\s*үлдэгдэл",
        r"closing\s*balance",
        r"эцэст\s*\(.*?\)\s*үлдэгдэл",
        r"үлдэгдэл\s*эцэст",
        r"эцсийн\s*дансны\s*үлдэгдэл",
    ]),
]


def extract_summary_amounts(raw: str) -> dict:
    """PDF/Excel-ийн text дотроос 'НИЙТ ОРЛОГО', 'НИЙТ ЗАРЛАГА', 'Эхний/Эцсийн үлдэгдэл'
    гэх мэт мөрнөөс жинхэнэ тоог олж буцаана.

    Зарим банк (Голомт г.м) дүнг keyword-ийн өмнө бичдэг ("4,662,900.00 НИЙТ ОРЛОГО"),
    зарим нь араас. Тиймээс хоёр талыг шалгана.
    """
    out: dict = {}
    if not raw:
        return out
    low = raw.lower()
    for key, patterns in SUMMARY_PATTERNS:
        for pat in patterns:
            for m in re.finditer(pat, low):
                # Эхний оролдлого: keyword-ийн өмнө 200 тэмдэгт (Голомт стиль)
                head_start = max(0, m.start() - 200)
                head = raw[head_start:m.start()]
                head_amounts = AMOUNT_RE.findall(head)
                for a in reversed(head_amounts):
                    v = normalize_amount(a)
                    if v is not None and abs(v) >= 100:
                        out[key] = abs(v)
                        break
                if key in out:
                    break
                # Хэрэв олдоогүй бол keyword-ийн араас үзнэ
                tail = raw[m.end(): m.end() + 200]
                tail_amounts = AMOUNT_RE.findall(tail)
                for a in tail_amounts:
                    v = normalize_amount(a)
                    if v is not None and abs(v) >= 100:
                        out[key] = abs(v)
                        break
                if key in out:
                    break
            if key in out:
                break
    return out


def parse_excel(content: bytes, ext: str) -> Tuple[str, List[ParsedTransaction]]:
    engine = None
    if ext == ".xls":
        engine = "xlrd"
    elif ext == ".xlsx":
        engine = "openpyxl"
    try:
        sheets = pd.read_excel(io.BytesIO(content), sheet_name=None, engine=engine, dtype=str)
    except Exception as exc:  # noqa: BLE001
        log.warning("excel read failed: %s", exc)
        return "", []

    txs: List[ParsedTransaction] = []
    text_parts: List[str] = []
    for _, df in sheets.items():
        df = df.fillna("")
        text_parts.append(df.to_csv(index=False, sep="|"))
        if df.empty:
            continue
        header = [str(c).strip().lower() for c in df.columns]
        for _, row in df.iterrows():
            tx = row_to_tx(header, [str(v) for v in row.tolist()])
            if tx:
                txs.append(tx)
    return "\n".join(text_parts), txs


def parse_csv(content: bytes) -> Tuple[str, List[ParsedTransaction]]:
    try:
        df = pd.read_csv(io.BytesIO(content), dtype=str)
    except Exception:
        try:
            df = pd.read_csv(io.BytesIO(content), dtype=str, sep=";")
        except Exception as exc:  # noqa: BLE001
            log.warning("csv read failed: %s", exc)
            return "", []
    df = df.fillna("")
    header = [str(c).strip().lower() for c in df.columns]
    txs = []
    for _, row in df.iterrows():
        tx = row_to_tx(header, [str(v) for v in row.tolist()])
        if tx:
            txs.append(tx)
    return df.to_csv(index=False, sep="|"), txs


# Header tokens → field name
HEADER_HINTS = {
    "date": ["date", "огноо", "гүйлгээний огноо", "transaction date", "огноо/цаг"],
    "description": ["description", "narrative", "тайлбар", "гүйлгээний утга", "purpose", "детайл", "details"],
    "credit": ["credit", "орлого", "deposit", "in"],
    "debit": ["debit", "зарлага", "withdrawal", "out"],
    "amount": ["amount", "дүн", "value"],
    "balance": ["balance", "үлдэгдэл"],
    "type": ["type", "төрөл"],
}


def header_index(header: List[str], hints: List[str]) -> Optional[int]:
    for idx, h in enumerate(header):
        for hint in hints:
            if hint in h:
                return idx
    return None


def row_to_tx(header: List[str], row: List[str]) -> Optional[ParsedTransaction]:
    if not row or all(not (c or "").strip() for c in row):
        return None

    idx_date = header_index(header, HEADER_HINTS["date"])
    idx_desc = header_index(header, HEADER_HINTS["description"])
    idx_credit = header_index(header, HEADER_HINTS["credit"])
    idx_debit = header_index(header, HEADER_HINTS["debit"])
    idx_amount = header_index(header, HEADER_HINTS["amount"])
    idx_balance = header_index(header, HEADER_HINTS["balance"])

    def get(i: Optional[int]) -> str:
        if i is None or i >= len(row):
            return ""
        return (row[i] or "").strip()

    date = normalize_date(get(idx_date))
    desc = get(idx_desc)
    balance = normalize_amount(get(idx_balance)) or 0.0

    amount = 0.0
    tx_type = "expense"

    if idx_credit is not None and get(idx_credit):
        v = normalize_amount(get(idx_credit))
        if v and v > 0:
            amount = v
            tx_type = "income"
    if idx_debit is not None and get(idx_debit) and amount == 0:
        v = normalize_amount(get(idx_debit))
        if v and v != 0:
            amount = abs(v)
            tx_type = "expense"
    if amount == 0 and idx_amount is not None and get(idx_amount):
        v = normalize_amount(get(idx_amount))
        if v is not None:
            amount = abs(v)
            tx_type = "income" if v > 0 else "expense"

    if amount == 0 and not date and not desc:
        return None
    if amount == 0:
        return None

    if not desc:
        # Бүх багана нэгтгэе
        desc = " ".join(c for c in row if c and c.strip())[:140]

    return ParsedTransaction(
        date=date,
        description=desc.strip(),
        amount=amount,
        type=tx_type,
        category=classify(desc),
        balance=balance,
    )


def line_to_tx(line: str) -> Optional[ParsedTransaction]:
    line = line.strip()
    if len(line) < 8:
        return None

    date_match = DATE_RE.search(line)
    if not date_match:
        return None
    date = normalize_date(date_match.group(0))

    amounts = [m.group(0) for m in AMOUNT_RE.finditer(line)]
    # Сүүлийн valid тоог дүн гэж үзнэ
    amount: Optional[float] = None
    for a in reversed(amounts):
        v = normalize_amount(a)
        if v is not None and abs(v) > 0:
            amount = v
            break
    if amount is None:
        return None

    tx_type = "expense"
    low = line.lower()
    if amount > 0 and ("credit" in low or "+" in line.split(date_match.group(0))[1][:5]):
        tx_type = "income"
    if amount < 0 or "debit" in low:
        tx_type = "expense"

    desc = (
        line.replace(date_match.group(0), "", 1)
        .strip()
    )
    for a in amounts:
        desc = desc.replace(a, "")
    desc = re.sub(r"\s+", " ", desc).strip(" -|")
    if not desc:
        desc = "—"

    return ParsedTransaction(
        date=date,
        description=desc[:140],
        amount=abs(amount),
        type=tx_type,
        category=classify(desc),
    )


# ===================== Aggregation =====================


MIN_TX_AMOUNT = 100.0  # 100₮-өөс бага noise-уудыг хаяна


def filter_noise(transactions: List[ParsedTransaction]) -> List[ParsedTransaction]:
    """Page numbers, мөрийн дугаар, date-ийн хагас, тайлбар хоосон тоонуудыг хаяна."""
    cleaned: List[ParsedTransaction] = []
    for t in transactions:
        if t.amount < MIN_TX_AMOUNT:
            continue
        desc = (t.description or "").strip()
        # Тайлбар нь зөвхөн тоо/огноо/хоосон бол алгасна
        if not desc or desc in {"—", "-"}:
            continue
        if re.fullmatch(r"[\d\s\-/.,]+", desc):
            continue
        cleaned.append(t)
    return cleaned


def aggregate(transactions: List[ParsedTransaction]) -> Tuple[float, float, List[CategoryBreakdown], str, str]:
    income = sum(t.amount for t in transactions if t.type == "income")
    expense = sum(t.amount for t in transactions if t.type == "expense")

    totals = defaultdict(float)
    counts = defaultdict(int)
    for t in transactions:
        if t.type != "expense":
            continue
        totals[t.category] += t.amount
        counts[t.category] += 1

    grand = sum(totals.values()) or 1.0
    breakdown = sorted(
        [
            CategoryBreakdown(
                category=cat,
                amount=amt,
                percentage=(amt / grand) * 100,
                count=counts[cat],
            )
            for cat, amt in totals.items()
        ],
        key=lambda c: c.amount,
        reverse=True,
    )

    dates = sorted([t.date for t in transactions if t.date])
    period_start = dates[0] if dates else ""
    period_end = dates[-1] if dates else ""

    return income, expense, breakdown, period_start, period_end


def derive_balances(transactions: List[ParsedTransaction], income: float, expense: float) -> Tuple[float, float]:
    """Хэрэв гүйлгээний balance байхгүй бол last/first дэх balance-ыг тооцоолно."""
    balances = [t.balance for t in transactions if t.balance]
    if not balances:
        return 0.0, income - expense
    return balances[0] - (income_of_first(transactions, "income") - income_of_first(transactions, "expense")), balances[-1]


def income_of_first(_transactions: List[ParsedTransaction], _type: str) -> float:
    # placeholder helper — opening balance цогц тооцооллыг service хэрэглэгчид өгөхгүй,
    # statement-аас direct-аар read-лж чадаагүй үед opening = 0 гэж үзнэ.
    return 0.0


# ===================== Endpoints =====================


@app.get("/")
def root():
    return {"service": "FinTrack Parser", "version": "1.0.0", "endpoints": ["/parse"]}


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/parse", response_model=ParsedStatement)
async def parse_statement(file: UploadFile = File(...)):
    name = (file.filename or "").lower()
    content = await file.read()
    if not content:
        raise HTTPException(status_code=400, detail="Empty file")

    if name.endswith(".pdf"):
        text, txs = parse_pdf(content)
    elif name.endswith(".xlsx") or name.endswith(".xls"):
        ext = ".xlsx" if name.endswith(".xlsx") else ".xls"
        text, txs = parse_excel(content, ext)
    elif name.endswith(".csv"):
        text, txs = parse_csv(content)
    else:
        raise HTTPException(status_code=400, detail="Зөвхөн PDF, Excel, CSV дэмжинэ")

    bank_name = detect_bank(text or "")
    txs = filter_noise(txs)

    # Эхлээд гүйлгээний нийлбэрээр тооцно...
    income, expense, breakdown, period_start, period_end = aggregate(txs)

    # ...дараа нь PDF/Excel-ийн "НИЙТ ОРЛОГО / ЗАРЛАГА / Үлдэгдэл" мөрийг
    # хайж олж, түүгээр нь override хийнэ. Энэ нь хэдэн зуун мөрийн нэгтгэлээс
    # илүү найдвартай.
    summary = extract_summary_amounts(text or "")
    if "total_income" in summary:
        income = summary["total_income"]
    if "total_expense" in summary:
        expense = summary["total_expense"]

    if "opening_balance" in summary:
        opening = summary["opening_balance"]
    else:
        opening, _ = derive_balances(txs, income, expense)
    if "closing_balance" in summary:
        closing = summary["closing_balance"]
    else:
        _, closing = derive_balances(txs, income, expense)

    # Хэрэв гүйлгээний нийлбэр нь summary-аас зөрж байгаа бол ангилал-ыг
    # дахин жинлэнэ — total_expense дотор % нь зөв байгаа эсэхийг хангана.
    if breakdown and expense > 0:
        sum_in_breakdown = sum(c.amount for c in breakdown) or 1.0
        scale = expense / sum_in_breakdown if sum_in_breakdown > 0 else 1.0
        for c in breakdown:
            c.amount = c.amount * scale
            c.percentage = (c.amount / expense) * 100

    log.info(
        "parsed bank=%s tx=%d income=%.0f expense=%.0f opening=%.0f closing=%.0f summary=%s",
        bank_name,
        len(txs),
        income,
        expense,
        opening,
        closing,
        list(summary.keys()),
    )

    return ParsedStatement(
        bank_name=bank_name,
        opening_balance=opening,
        closing_balance=closing,
        total_income=income,
        total_expenses=expense,
        period_start=period_start,
        period_end=period_end,
        transactions=txs,
        category_breakdown=breakdown,
    )
