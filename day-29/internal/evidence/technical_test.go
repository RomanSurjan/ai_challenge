package evidence

import "testing"

func TestTechnicalContract(t *testing.T) {
	final, candidates := contexts()
	v := Validator{MinQuoteRunes: 20, MaxQuoteRunes: 500, MaxClaims: 3, RequireInline: true, AllowInsufficient: true}
	r := validResponse()
	if x := v.Validate(r, final, candidates, nil); !x.Valid {
		t.Fatalf("valid rejected: %v", x.Errors)
	}
	r.Answer = "Некоторая информация отсутствует"
	r.Status = StatusInsufficientContext
	r.Claims = nil
	r.Evidence = nil
	r.ClarificationQuestion = "Какой компонент?"
	if x := v.Validate(r, final, candidates, nil); !x.Valid {
		t.Fatal(x.Errors)
	}
	r = validResponse()
	r.Evidence[0].Quote = "Несуществующая дословная цитата источника"
	if x := v.Validate(r, final, candidates, nil); x.Valid {
		t.Fatal("invented quote accepted")
	}
	r = validResponse()
	r.Answer = r.Claims[0].Text
	if x := v.Validate(r, final, candidates, nil); x.Valid {
		t.Fatal("missing inline accepted")
	}
	r = validResponse()
	r.Claims[0].SourceIDs = []string{"S99"}
	if x := v.Validate(r, final, candidates, nil); x.Valid {
		t.Fatal("unknown ID accepted")
	}
}

func TestTechnicalRequiredFields(t *testing.T) {
	_, err := ParseTechnical(`{"status":"insufficient_context","answer":"Нет данных","clarification_question":"Какой файл?"}`)
	if err == nil {
		t.Fatal("missing claims accepted")
	}
	_, err = ParseTechnical(`{"status":"insufficient_context","answer":"Нет данных","claims":[],"clarification_question":"Какой файл?"}`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDraftKeepsEvidenceValidation(t *testing.T) {
	final, candidates := contexts()
	raw := `{"status":"answered","claims":[{"id":"C1","text":"Первый технический факт подтверждён","evidence":[{"source_id":"S1","quote":"Unicode цитата точно находится в первом чанке"}]}],"clarification_question":""}`
	r, e := ParseDraft(raw)
	if e != nil {
		t.Fatal(e)
	}
	v := Validator{MinQuoteRunes: 20, MaxQuoteRunes: 500, MaxClaims: 3, RequireInline: true, AllowInsufficient: true}
	if x := v.Validate(r, final, candidates, nil); !x.Valid {
		t.Fatal(x.Errors)
	}
	if r.Answer != "Первый технический факт подтверждён [S1]" {
		t.Fatal(r.Answer)
	}
	r.Evidence[0].Quote = "Выдуманная достаточно длинная цитата"
	if x := v.Validate(r, final, candidates, nil); x.Valid {
		t.Fatal("draft invented quote accepted")
	}
	r, e = ParseDraft(`{"status":"answered","claims":[{"id":"C1","text":"Факт","evidence":[{"source_id":"S99","quote":"Unicode цитата точно находится в первом чанке"}]}],"clarification_question":""}`)
	if e != nil {
		t.Fatal(e)
	}
	if x := v.Validate(r, final, candidates, nil); x.Valid {
		t.Fatal("unknown draft source accepted")
	}
}
