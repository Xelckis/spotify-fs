from imports import hashlib, random
from spotify_cliente import spotifyclient

class verificador:
    def __init__(self, client,senha):
        #aqui ele criptografa a senha em hash hexadecimal
        hash_senha = hashlib.sha256(senha.encode("utf-8")).hexdigest()

        #aqui ele transforma hexadecimal em inteiro. Conhecimento novo!
        #o segundo argumento é dizendo em qual base foi escrita originalmente
        semente = int(hash_senha,16)
        #aqui ele tira a aleatoriedade da semente, dizendo que o que deve ser usado como seed
        random.seed(semente)

    def gerar_tabela(self):
