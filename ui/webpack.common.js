const MonacoWebpackPlugin = require('monaco-editor-webpack-plugin');
const path = require('path');

module.exports = {
  entry: './src/index.tsx',
  module: {
    rules: [
      {
        test: /\.css$/,
        use: ['style-loader', 'css-loader', 'postcss-loader'],
      },
      {
        test: /\.(png|jpg|gif)$/i,
        use: [
          {
            loader: 'url-loader',
            options: {
              encoding: 'base64',
            },
          },
        ],
      },
      {
        test: /\.ico$/i,
        type: 'asset/resource',
        generator: {
          filename: '[name][ext]',
        },
      },
      {
        test: /\.ttf$/,
        type: 'asset/resource',
      },
    ],
  },
  plugins: [
    new MonacoWebpackPlugin({
      filename: '[name].[contenthash:16].worker.js',
      languages: ['yaml'],
      features: ['find'],
      customLanguages: [
        {
          label: 'yaml',
          entry: 'monaco-yaml',
          worker: {
            id: 'monaco-yaml/yamlWorker',
            entry: 'monaco-yaml/yaml.worker',
          },
        },
      ],
    }),
  ],
  resolve: {
    extensions: ['.tsx', '.ts', '.js', '.d.ts'],
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
};
