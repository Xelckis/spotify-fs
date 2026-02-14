![Spotify-fs](../header.png)

| [🇺🇸 English](../README.md) | 🇪🇸 Español | [🇧🇷 Português](README.pt.md)

**spotify-fs** es una herramienta de Prueba de Concepto (PoC) escrita en Go que le permite almacenar archivos arbitrarios dentro de las listas de reproducción de Spotify.

Funciona transformando datos binarios en una secuencia de pista de Spotify. Básicamente, asigna valores de bytes (0-255) a canciones específicas y los organiza en una lista de reproducción para representar el archivo.

> ⚠️ **DESCARGO DE RESPONSABILIDAD:** Este proyecto es solo para fines educativos y de investigación. Almacenar datos en listas de reproducción probablemente infrinja las Condiciones de Sercivio de Spotify. El autor no se responsabiliza por cuentas bloqueadas ni por la pérdida de datos. Úselo bajo su proprio riesgo.

## 🚀 Características

- **Mapeo Cifrado/Sembrado:** Utiliza una contraseña para generar un diccionario único que asigna bytes a las pistas. Sin la contraseña (y el mapa del decodificador generado), la lista de reproducción parece una colección aleatoria de canciones.
- **Fragmentación y Encadenamiento:** Divide automáticamente archivos grandes en varias listas de reproducción si superan el límite de pistas. Las listas de reproducción se vinculan entre sí mediante sus campos de descripción.
- **Concurrencia:** Utiliza varios trabajadores para acelerar los procesos de escritura (agregar pistas) y lectura (obtener pistas).
- **Manejo de Límite de Velocidad:** Retrocede automáticamente y vuelve a intentarlo cuando alcanza los límites de velocidad de la API de Spotify (429) o errores de puerta de enlace (502).

## 🛠️ Requisitos Previos

- **Go:** Versión 1.25 o superior.
- **Cuenta de Spotify:** Necesaria para acceder a la API y modificar las listas de reproducción eficazmente.
- **Aplicación para Desarrolladores de Spotify:** Necesita un ID de cliente y una clave secreta de secreta de cliente.

## ⚙️ Configuración

### 1. **Clonar el repositorio:**

```bash
    git clone [https://github.com/xelckis/spotify-fs.git](https://github.com/xelckis/spotify-fs.git)
    cd spotify-fs
```

### 2. Crea una app de Spotify:

 - Ve al Panel de Desarrollo de Spotify.
 - Crea una app y configura la URI de redireccionamiento a: http://127.0.0.1:8080/callback/spotify

### 3. Establecer variables de entorno: Debe exportar sus credenciales antes de ejecutar la herramienta:

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

Ejecute la aplicación:
```bash
go run main.go
```
Siga las instrucciones interactivas en pantalla.

### 1. Escribir un Archivo (Cargar)

Seleccione la opción 1.

 1. Filepath: Ruta al archivo que desea cargar.

 2. Playlist Name: El nombre base de la(s) lista(s) de reproducción.

 3. Contraseña: Se utiliza para iniciar la generación aleatoria del diccionario byte-a-pista.

  The tool will:

 - Autentíquese a través de su navegador.

 - Crea un archivo [PlaylistName]_Decoder.gob localemnte (¡mantenlo seguro! Ayuda a acelerar la lectura).

 - Sube los datos a Spotify.

### 2. Leyendo un Archivo (Descargar)

Seleccione la opción 2.

 1. Playlist ID: El ID de la primera lista de reproducción de la cadena (que se encuentra en la URL de Spotify).

 2. Nombre del Archivo de Salida: Nombre (incluida la extensión) para guardar el archivo restaurado.

 3. Decoder Path (Opcional): Ruta al archivo _Decoder.gob generado durante la carga. Si se omite, la herramienta intenta regenerar el mapa usando la contraseña (más lento).

 4. Contraseña: Debe coincidir con la utilizada durante la carga.

## 🔧 Detalles Técnicos

 - Generación de Diccionario: La herramienta busca pistas aleatorias en Spotify basándose en una semilla derivada de tu contraseña. Asigna una URI de pista única a cada valor de byte (de 0x00 a 0xFF).

 - Almacenamiento: El archivo se lee en fragmentos. Cada byte se convierte a su URI de pista correspondiente y se añade a una lista de reproducción.

 - Lista Enlazada: Si un archivo es demasiado grande para una lista de reproducción, se crea una nueva. El ID de la siguiente lista se almacena en la descripción de la lista actual, formando así una lista enlazada.

## Licencia

Este proyecto está licenciado bajo la  [Licencia Apache-2.0](https://opensource.org/license/apache-2-0) - consulte el archivo [LICENSE](../LICENSE) para obtener más detalles.