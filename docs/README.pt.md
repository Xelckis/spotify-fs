![Spotify-fs](../header.png)

| [🇺🇸 English](../README.md) | [🇪🇸 Español](README.es.md) | 🇧🇷 Português

**spotify-fs** é uma ferramenta de Prova de Conceito (PoC) escrita em Go que permite armazenar arquivos arbitrários dentro de playlists do Spotify.

Ele funciona transformando dados binários em uma sequência de faixas do Spotify. Essencialmente, mapeia valores de bytes (0-255) para músicas específicas e as organiza em uma lista de reprodução para representar o arquivo.

> ⚠️ **AVISO LEGAL:** Este projeto destina-se apenas a fins educacionais e de pesquisa. O armazenamento de dados em playlists provavelmente viola os Termos de Serviço do Spotify. O autor não se responsabiliza por contas banidas ou perda de dados. Use por sua conta e risco.

## 🚀 Recursos

- **Mapeamento Criptografado/Com Semente:** Usa uma senha para gerar um dicionário exclusivo que mapeia bytes para faixas. Sem a senha (e o mapa decodificador gerado), a lista de reprodução parece apenas uma coleção aleatória de músicas.
- **Fragmentação e Encadeamento:** Divide automaticamente arquivos grandes em várias listas de reprodução caso excedam o limite de faixas. As listas de reprodução são vinculadas entre si por meio de seus campos de descrição.
- **Concorrência:** Utiliza múltiplos processos para acelerar as operações de escrita (adição de faixas) e leitura (busca de faixas).
- **Gerenciamento de Limites de Taxa:** Recua automaticamente e tenta novamente ao atingir os limites de taxa da API do Spotify (429) ou erros de gateway (502).

## 🛠️ Pré-Requisitos

- **Go:** Versão 1.25 ou superior.
- **Conta Spotify:** Necessária para acesso à API e para modificar playlists de forma eficaz.
- **Aplicativo de Desenvolvedor Spotify:** Você precisa de um ID do cliente e uma chave secreta do cliente.

## ⚙️ Configuração

### 1. **Clone o repositório:**

```bash
    git clone [https://github.com/xelckis/spotify-fs.git](https://github.com/xelckis/spotify-fs.git)
   cd spotify-fs
```

### 2. Criar um Aplicativo do Spotify:

 - Acesse o Painel de Desenvolvedores do Spotify.
 - Crie um aplicativo e defina o URI de redirecionamento para: http://127.0.0.1:8080/callback/spotify

### 3. Configurar Variáveis de Ambiente: Você precisa exportar suas credenciais antes de executar a ferramenta:

Linux/macOS:
```bash
export SPOTIFY_CLIENTID="your_client_id_here"
export SPOTIFY_CLIENTSECRET="your_client_secret_here"
```
Windows (PowerShell):
```PowerShell
$env:SPOTIFY_CLIENTID="your_client_id_here"
$env:SPOTIFY_CLIENTSECRET="your_client_secret_here"
```

## 📦 Uso

Execute o aplicativo:
```bash
go run main.go
```
Siga as instruções interativas na tela.

### 1. Gravando um Arquivo (Upload)

Selecione a opção 1.

 1. Filepath: Caminho para o arquivo que você deseja enviar.

 2. Playlist Name: O nome base da(s) lista(s) de reprodução.

 3. Senha: Usada para inicializar a geração aleatória do dicionário byte-para-trilha.

  A ferramenta irá:

 - Autentique-se através do seu navegador.

 - Crie um arquivo [PlaylistName]_Decoder.gob localmente (guarde-o em segurança! Isso ajuda a acelerar a leitura).

 - Envie os dados para o Spotify.

### 2. Lendo um Arquivo (Download)

Selecione a opção 2.

 1. Playlist ID: O ID da primeira playlist da sequência (encontrado no URL do Spotify).

 2. Nome do Arquivo de Saída: Nome (incluindo a extensão) para salvar o arquivo restaurado.

 3. Decoder Path (Opcional): Caminho para o arquivo _Decoder.gob gerado durante o upload. Se omitido, a ferramenta tentará regenerar o mapa usando a senha (mais lento).

 4. Senha: Deve ser a mesma usada durante o upload.

## 🔧 Detalhes Técnicos

 - Geração de Dicionário: A ferramenta pesquisa faixas aleatórias no Spotify com base em uma semente derivada da sua senha. Ela atribui um URI de faixa exclusivo a cada valor de byte (0x00 a 0xFF).

 - Armazenamento: O arquivo é lido em partes. Cada byte é convertido em seu URI de faixa correspondente e adicionado a uma lista de reprodução.

 - Lista Encadeada: Se um arquivo for muito grande para uma lista de reprodução, uma nova será criada. O ID da próxima lista de reprodução é armazenado na descrição da lista de reprodução atual, formando uma lista encadeada.

## Licença

Este projeto está licenciado sob a [Licença Apache-2.0](https://opensource.org/license/apache-2-0) - veja o arquivo [LICENSE](../LICENSE) para detalhes.