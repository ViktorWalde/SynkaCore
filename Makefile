# Portoes de qualidade do SynkaCore.
#
# A V1.x impunha disciplina pela ferramenta e nao pela boa vontade — compilador em
# modo rigoroso, analise estatica, estilo, tudo derrubando o build. Esse principio
# atravessou a reescrita; mudaram apenas as ferramentas.
#
# Regra: se uma regra so vale quando alguem lembra, ela sera quebrada.

MODULO      := github.com/ViktorWalde/SynkaCore
BINARIOS    := bin
CONTRATO    := contrato/proto
GERADO      := internal/contrato/v1

# CGO desligado produz binario VERDADEIRAMENTE estatico, sem dependencia de
# runtime. E o argumento que decidiu a linguagem: implantar e copiar UM arquivo e
# uma unidade systemd, e reverter e manter o arquivo anterior. Numa planta sem
# internet, atualizada pelo notebook de um tecnico, isso vale mais que qualquer
# vantagem de velocidade bruta.
#
# Definido por ALVO, e nao exportado para o Makefile inteiro, por um motivo que
# custou uma tentativa para aparecer: o detector de corrida do Go EXIGE cgo. Com
# CGO_ENABLED=0 global, `go test -race` recusa rodar — e a alternativa de largar o
# -race para manter uma variavel arrumada seria trocar a verificacao de
# concorrencia por conveniencia de build. O no roda dois lacos concorrentes sobre
# um buffer compartilhado; abrir mao do -race ali nao esta em questao.
CGO_PARA_BINARIO := CGO_ENABLED=0
CGO_PARA_TESTE   := CGO_ENABLED=1

.PHONY: tudo
tudo: verificar compilar

# ---------------------------------------------------------------- compilacao

.PHONY: compilar
compilar:
	$(CGO_PARA_BINARIO) go build -trimpath -ldflags='-s -w' -o $(BINARIOS)/synkacore-gateway ./cmd/synkacore-gateway
	$(CGO_PARA_BINARIO) go build -trimpath -ldflags='-s -w' -o $(BINARIOS)/synkacore-no ./cmd/synkacore-no
	$(CGO_PARA_BINARIO) go build -trimpath -ldflags='-s -w' -o $(BINARIOS)/synkacore-credencial ./cmd/synkacore-credencial
	$(CGO_PARA_BINARIO) go build -trimpath -ldflags='-s -w' -o $(BINARIOS)/synkacore-carga ./ferramentas/geradordecarga

.PHONY: limpar
limpar:
	rm -rf $(BINARIOS) cobertura.out cobertura.html

# ---------------------------------------------------------------- verificacao

# verificar e o portao completo. Tudo que roda aqui derruba o build ao falhar.
.PHONY: verificar
verificar: formatar-conferir vet testar linter contrato-conferir no-micropython-conferir

.PHONY: testar
testar:
	$(CGO_PARA_TESTE) go test -race -count=1 ./...

.PHONY: cobertura
cobertura:
	$(CGO_PARA_TESTE) go test -race -count=1 -coverprofile=cobertura.out ./...
	go tool cover -html=cobertura.out -o cobertura.html
	@echo "relatorio em cobertura.html"

.PHONY: vet
vet:
	go vet ./...

.PHONY: linter
linter:
	golangci-lint run

.PHONY: formatar
formatar:
	gofmt -w .

# Falha se algum arquivo estiver fora do formato canonico. Formatacao divergente
# polui todo diff futuro e esconde a mudanca real dentro do ruido.
.PHONY: formatar-conferir
formatar-conferir:
	@divergentes=$$(gofmt -l .); \
	if [ -n "$$divergentes" ]; then \
		echo "arquivos fora do formato canonico:"; echo "$$divergentes"; exit 1; \
	fi

# ---------------------------------------------------------------- contrato

# contrato regera o codigo a partir do .proto.
#
# O gerado E VERSIONADO de proposito: o projeto precisa compilar com `go build`
# puro, sem protoc instalado, numa planta sem internet.
.PHONY: contrato
contrato:
	protoc --proto_path=$(CONTRATO) \
		--go_out=. --go_opt=module=$(MODULO) \
		$(CONTRATO)/synkacore/contrato/v1/aquisicao.proto
	gofmt -w $(GERADO)

# contrato-conferir reprova o build se o gerado estiver desatualizado em relacao ao
# .proto.
#
# Sem esta trava, alguem edita o contrato, esquece de regerar, e o binario passa a
# falar uma versao do contrato que nao e a documentada — divergencia que so aparece
# quando uma origem em campo manda o campo novo.
#
# Pulado com um aviso quando protoc nao esta instalado, para nao inviabilizar o
# build de quem so quer compilar.
.PHONY: contrato-conferir
contrato-conferir:
	@if ! command -v protoc >/dev/null 2>&1; then \
		echo "protoc ausente: conferencia do contrato pulada"; exit 0; \
	fi; \
	cp $(GERADO)/aquisicao.pb.go /tmp/synkacore-contrato-anterior.go; \
	$(MAKE) --no-print-directory contrato >/dev/null; \
	if ! diff -q /tmp/synkacore-contrato-anterior.go $(GERADO)/aquisicao.pb.go >/dev/null; then \
		echo "o codigo gerado esta desatualizado: rode 'make contrato' e versione o resultado"; \
		exit 1; \
	fi; \
	echo "contrato em dia"

# ---------------------------------------------------------------- execucao

# infra sobe apenas o estagio de CONSULTA. O gateway funciona sem ele.
.PHONY: infra
infra:
	docker compose up -d

.PHONY: infra-parar
infra-parar:
	docker compose down

# gateway sobe sem banco de consulta: aquisicao completa, sem projecao. E o modo em
# que o sistema e exercitavel sem nenhuma infraestrutura.
.PHONY: gateway
gateway: compilar
	./$(BINARIOS)/synkacore-gateway

# gateway-completo liga a projecao para o TimescaleDB local.
.PHONY: gateway-completo
gateway-completo: compilar
	./$(BINARIOS)/synkacore-gateway \
		-banco 'postgres://synkacore:synkacore@127.0.0.1:5432/synkacore' \
		-instalacao configuracao/instalacao.exemplo.yaml

.PHONY: no
no: compilar
	./$(BINARIOS)/synkacore-no

# ---------------------------------------------------------------- medicao

# DISCO_DE_MEDICAO precisa apontar para disco PERSISTENTE, nunca tmpfs.
#
# Isto custou uma medicao inteira jogada fora: b.TempDir() segue o TMPDIR, e onde
# /tmp e tmpfs o fsync nunca alcanca midia fisica. Os numeros sao reais e medem a
# RAM — o lote unitario aparecia 7,5x mais rapido do que e.
#
# Publicar isso como capacidade seria pior que nao medir: alguem dimensionaria uma
# instalacao com um numero que o disco nunca vai entregar.
DISCO_DE_MEDICAO ?= $(HOME)/.cache/synkacore-medicao

.PHONY: medir
medir:
	@mkdir -p $(DISCO_DE_MEDICAO)
	@df -T $(DISCO_DE_MEDICAO) | tail -1 | grep -q tmpfs && { \
		echo "$(DISCO_DE_MEDICAO) esta em tmpfs: a medicao descreveria a RAM, nao o diario."; \
		echo "Defina DISCO_DE_MEDICAO para uma pasta em disco persistente."; exit 1; } || true
	SYNKACORE_DISCO_DE_MEDICAO=$(DISCO_DE_MEDICAO) \
		$(CGO_PARA_TESTE) go test -run='^$$' -bench=. -benchtime=20x \
		./internal/adaptador/saida/diariosqlite/

# carga responde "quantos dispositivos o gateway aguenta?" contra um gateway no ar.
#
# Exige o gateway rodando: ele mede o caminho INTEIRO — serializacao, rede, contencao
# no diario — e nao a chamada em processo, que deixaria de fora justamente os
# candidatos a gargalo.
.PHONY: carga
carga: compilar
	./$(BINARIOS)/synkacore-carga -origens $(or $(ORIGENS),50) -lote $(or $(LOTE),100) \
		-intervalo $(or $(INTERVALO),1s) -duracao $(or $(DURACAO),30s)

# ---------------------------------------------------------------- no micropython

# no-micropython regera o codificador protobuf do no a partir do contrato.
#
# O gerado E VERSIONADO, como o do gateway: o ESP32 recebe o arquivo por cabo, e
# quem o copia nao tem Go instalado.
.PHONY: no-micropython
no-micropython:
	go run ./ferramentas/geradordenopython -saida no-micropython/synkacore_contrato.py
	@echo "gerado em no-micropython/synkacore_contrato.py"

# no-micropython-conferir reprova o build se o codificador do no estiver
# desatualizado em relacao ao .proto.
#
# Sem esta trava, alguem edita o contrato, esquece de regerar, e o no passa a falar
# uma versao que o gateway nao espera — divergencia que so aparece com o ESP32 em
# campo. O teste de fidelidade compara os BYTES; esta conferencia garante que os
# bytes comparados vem do contrato atual.
.PHONY: no-micropython-conferir
no-micropython-conferir:
	@cp no-micropython/synkacore_contrato.py /tmp/synkacore-no-anterior.py
	@$(MAKE) --no-print-directory no-micropython >/dev/null
	@if ! diff -q /tmp/synkacore-no-anterior.py no-micropython/synkacore_contrato.py >/dev/null; then \
		echo "o codificador do no esta desatualizado: rode 'make no-micropython' e versione o resultado"; \
		exit 1; \
	fi; \
	echo "codificador do no em dia"

# ---------------------------------------------------------------- proveniencia

# manifesto produz o resumo criptografico reprodutivel do codigo-fonte.
#
# Para que serve: e o artefato que um registro de programa de computador (INPI,
# Lei 9.609/1998) consome. O sistema NAO publica o codigo — publica o resumo. Quem
# guarda o codigo e voce; o hash e o que permite provar, depois, que o que voce
# tem e o que foi registrado.
#
# Serve tambem fora do registro formal: e uma ancora de conteudo independente do
# git. Historico de git pode ser reescrito, e por isso ele e evidencia
# CORROBORANTE, nao prova por si so. Um hash publicado ou registrado numa data nao
# pode ser refeito depois.
#
# Determinismo, e como ele e obtido:
#
#   - a lista de arquivos vem do git (versionados + nao ignorados), nunca de um
#     `find`, que traria artefato de build e variaria por maquina;
#   - a ordenacao e por LC_ALL=C, que e estavel entre sistemas e locales — sem
#     isso, a mesma arvore produziria ordens diferentes em pt_BR e en_US, e o hash
#     raiz mudaria sem nenhuma alteracao de codigo;
#   - o hash raiz e calculado sobre a lista de hashes, e nao sobre os arquivos
#     concatenados, para que renomear um arquivo tambem altere o resultado;
#   - o proprio MANIFESTO.txt fica FORA da lista. Sem isso ele se incluiria, e
#     gerar o manifesto mudaria a arvore que o manifesto descreve: a conferencia
#     jamais fecharia, porque o arquivo lido nao existia quando ele foi calculado.
.PHONY: manifesto
manifesto:
	@{ \
		echo "# MANIFESTO DE CODIGO-FONTE — SynkaCore"; \
		echo "#"; \
		echo "# Gerado por 'make manifesto'. Reproduzivel: a mesma arvore produz o"; \
		echo "# mesmo resultado em qualquer maquina."; \
		echo "#"; \
		echo "# commit:  $$(git rev-parse HEAD 2>/dev/null || echo 'arvore sem commit')"; \
		echo "# arvore:  $$(git status --porcelain | wc -l) arquivo(s) com alteracao nao commitada"; \
		echo "# gerado:  $$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
		echo "#"; \
		echo "# ATENCAO: 'gerado' e a data desta execucao e NAO prova nada sozinha —"; \
		echo "# a data do sistema e ajustavel. O que ancora a data e o registro"; \
		echo "# externo onde o hash raiz for depositado."; \
		echo ""; \
		git ls-files --cached --others --exclude-standard | grep -v '^MANIFESTO.txt$$' | \
			LC_ALL=C sort | xargs -r sha256sum; \
	} > MANIFESTO.txt
	@echo "" >> MANIFESTO.txt
	@echo "# HASH RAIZ (sha256 sobre a lista de hashes acima):" >> MANIFESTO.txt
	@grep -E '^[0-9a-f]{64}  ' MANIFESTO.txt | sha256sum | \
		awk '{print "# " $$1}' >> MANIFESTO.txt
	@echo "manifesto gerado em MANIFESTO.txt"
	@tail -3 MANIFESTO.txt

# manifesto-conferir refaz o calculo e compara com o MANIFESTO.txt versionado.
#
# Serve para responder, meses depois, "este codigo e o que foi registrado?" sem
# depender de memoria nem de confianca.
.PHONY: manifesto-conferir
manifesto-conferir:
	@if [ ! -f MANIFESTO.txt ]; then echo "MANIFESTO.txt ausente: rode 'make manifesto'"; exit 1; fi
	@registrado=$$(grep -A1 'HASH RAIZ' MANIFESTO.txt | tail -1 | tr -d '# '); \
	atual=$$(git ls-files --cached --others --exclude-standard | grep -v '^MANIFESTO.txt$$' | \
		LC_ALL=C sort | xargs -r sha256sum | sha256sum | awk '{print $$1}'); \
	if [ "$$registrado" = "$$atual" ]; then \
		echo "confere: $$atual"; \
	else \
		echo "DIVERGE"; echo "  registrado: $$registrado"; echo "  atual:      $$atual"; exit 1; \
	fi
