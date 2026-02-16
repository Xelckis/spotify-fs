import time
import urllib.parse
import json
import requests
import webbrowser
from http.server import BaseHTTPRequestHandler, HTTPServer

#ele ler o arquivo json com as informações de login
with open("informacoes.json", "r", encoding="utf-8") as arquivo:
    dados = json.load(arquivo)

#o que vai ser requesitado
permi_spotify = ["playlist-read-private","playlist-read-collaborative","playlist-modify-private","playlist-modify-public"]

def gera_link(id_client,url_redirecionamento,permi_spotify):
    #url base que vai ser usada para depois fundir com os parametros, ja fiz coisa parecida nos trabalhos de webscrapping da ufc
    destino_base = r"https://accounts.spotify.com/authorize"

    escopo_formatado = " ".join(permi_spotify)

    parametros = {
        "client_id":id_client,
        "response_type":"code",
        "redirect_uri":url_redirecionamento,
        "scope":escopo_formatado,
        "show_dialog": "true"
    }
    #juntando a url base com os parametros
    url_formatada = f"{destino_base}?{urllib.parse.urlencode(parametros)}"

    return url_formatada

def troca_codigo_p_token(code,id_client,id_client_secret,url_redirecionamento):
    destino_base = r"https://accounts.spotify.com/api/token"

    #não entendi como funciona esse payload, sendo que nao tem o uso da urllib pra juntar os links
    #explicação do futuro: a função post ja tem um empacotador embutido, por isso que nao precisa do urlib

    #aqui é os dados que vai passar na url pra fazer a autenticação e conseguir fazer a troca
    payload = {
        "grant_type": "authorization_code",
        "code":code,
        "redirect_uri":url_redirecionamento,
        "client_id": id_client,
        "client_secret": id_client_secret
    }

    #inicia a troca, por meio de um requests.post. É algo parecido com que tem no código do cara la
    response = requests.post(destino_base, payload)

    if response.status_code == 200:
        #verifica se o token foi recebido de forma certa usando status_code
        dados_resp = response.json()
        print("Token recebido, sucesso!")
        return dados_resp["access_token"]

    else:
        print(f"erro ao carregar o token {response.text}")
        return None

class spotifyclient:
    def __init__(self,token):
        #ele passa o heards com o token
        self.token = token
        self.headers = {
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json"
            #content type é apenas avisando ao spotify o que ta sendo enviado pra ele
        }

    def _faz_requisicao(self,metodo,url, **kwargs):
        while True:
            #o metodo pode ser um get, post ou put
            #url é o endereço
            #headers é o token que ja foi pegue nessa altura do campeonato
            #kwargs ele se adapta de acordo com a situação, deixa o código mais escalavel, mais generico, pronto para varias situacoes

            response = requests.request(metodo,url,headers=self.headers, **kwargs)

            if response.status_code == 429:
                tempo_fila = int(response.headers.get("Retry-After",5))
                print(f"entrando na fila, requisições maximas atingidas!\nesperando {tempo_fila} segundos")
                time.sleep(tempo_fila+1)
                continue

            return response

    def get_user_id(self):
        #não entendi como funciona essa função
        url = "https://api.spotify.com/v1/me"

        response = self._faz_requisicao("GET", url)

        if response.status_code == 200:
            dados_user = response.json()
            return dados_user["id"]
        else:
            print(f"Erro ao pegar perfil: {response.status_code}")
            return None


# Variável global para guardar o código temporariamente
# que código é esse?
codigo_capturado = None

#essa classe é uma classe filha de uma outra classe criada na biblioteca http.server
class gerenciaresposta(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        return # Silencia logs do servidor para não poluir o terminal

    def do_GET(self):
        global codigo_capturado
        #o if vai rolar o endereço que o naveador acessou
        # se dentro do endereço tiver esse endereço ai do spotify, ele entra
        if "/callback/spotify" in self.path:
            #ele divide a url para achar aonde ta escrito code

            #ele começa cortando e pegando apenas o que ta depois da interrogação
            query = urllib.parse.urlparse(self.path).query
            #aqui ele corta mais uma vez usando o & como caractere de referencia e cria dois pedaços
            #depois ele pega esses dois valores e usa = como caractere de referencia, deixando o que ta do lado esquerdo chave e lado direito valor
            query_components = urllib.parse.parse_qs(query)

            #quando acha o code, ele passa uma mensagem siples em HTML no naveador confirmando que pode fehcar
            if "code" in query_components:
                #querycomponents é um dicionario que é criado depois do
                #o query retorna uma lista, no lado direito, lado valor. Sendo assim é dito para ele pegar o primeiro item e único da lista
                codigo_capturado = query_components["code"][0]
                self.send_response(200)
                self.send_header("Content-type", "text/html")
                self.end_headers()
                self.wfile.write(b"<h1>Sucesso! Pode fechar. Volte ao Python.</h1>")
                self.wfile.write(b"<script>window.close();</script>")
            else:
                self.send_error(400, "Codigo nao encontrado")

#TODO só copiei e colei preciso estudar melhor esse código ---> atualização: entendi ja como funciona e está comentado

def login_automatico(client_id, client_secret, redirect_uri, escopos):
    # 1. Gera o link usando a função que foi criada antes
    link = gera_link(client_id, redirect_uri, escopos)

    # 2. Abre o navegador automaticamente
    print("Abrindo navegador para login...")
    webbrowser.open(link)

    # 3. Inicia o servidor local para esperar a resposta
    # Isso trava o programa aqui até o Spotify responder na porta 8080
    server_address = ('127.0.0.1', 8080)
    httpd = HTTPServer(server_address, gerenciaresposta)

    print("Aguardando resposta do Spotify (callback)...")
    # O handle_request atende UMA única vez e depois solta o código
    httpd.handle_request()

    if codigo_capturado:
        print(f"Código capturado!")
        return troca_codigo_p_token(codigo_capturado, client_id, client_secret, redirect_uri)
    else:
        print("Falha ao capturar o código.")
        return None

if __name__ == "__main__":
    # Garanta que no JSON as chaves sejam exatamente "id_client" e "id_client_secret"
    token = login_automatico(dados["id_client"], dados["id_client_secret"], dados["url_redirecionamento"], permi_spotify)

    if token:
        # Agora o nome da classe bate com a definição lá em cima
        cliente = spotifyclient(token)
        user_id = cliente.get_user_id()
        print(f"Autenticado: {user_id}")