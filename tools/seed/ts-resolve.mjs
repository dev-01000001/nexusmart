// Resolve extensionless relative imports to .ts so Node's type stripping can load web/src/data.
export async function resolve(specifier, context, next) {
  try {
    return await next(specifier, context);
  } catch (err) {
    if (specifier.startsWith(".")) return next(specifier + ".ts", context);
    throw err;
  }
}
