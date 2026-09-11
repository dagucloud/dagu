# UI

## Prerequisites

- Go 1.27.0 or newer
- Node.js 18.18 or newer
- pnpm

## Development Instructions

### 1. Starting the Backend Server

The Dagu UI relies on a backend server that provides the necessary data for the UI to function properly. From the repository root, run:

```bash
make run-server
```

This command will start the backend server at 127.0.0.1:8080 by default. If you need to use a different address or port, you can modify the appropriate settings in the backend configuration file.

### 2. Starting the Webpack Dev Server

Once the backend server is up and running, you can start the Webpack dev server to serve the frontend assets. To do this, navigate to the ui/ directory and execute the following commands:

```bash
cd ui/
pnpm install --frozen-lockfile
pnpm dev
```

This command will start the Webpack dev server at `127.0.0.1:8081`. You can access the UI by opening your web browser and navigating to http://localhost:8081.

### 3. Building the Bundle.js File

If you need to build the `bundle.js` file, which contains all the necessary frontend assets, you can do so using the following command:

```
cd ../
make ui
```

This command will build the `bundle.js` file and copy it to dagu/frontend/assets/js/bundle.js. This is necessary for the Go backend to include the JavaScript within the binary.
