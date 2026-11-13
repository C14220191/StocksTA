import { useState } from "react";

// State form akun. bind(nama) -> props <Field> (value, onChange, error). run(fn) memanggil API dengan
// nilai form: kesalahan per isian dipasang di isiannya (hilang begitu isian diubah), kesalahan lain
// menjadi message { text, code, retryAfter } di atas form. Mengembalikan hasil API, atau undefined
// kalau gagal.
export default function useForm(initial) {
  const [values, setValues] = useState(initial);
  const [errors, setErrors] = useState({});
  const [message, setMessage] = useState(null);
  const [busy, setBusy] = useState(false);

  const bind = (name) => ({
    value: values[name],
    error: errors[name],
    onChange: (e) => {
      const v = e.target.value;
      setValues((old) => ({ ...old, [name]: v }));
      setErrors((old) => (old[name] ? { ...old, [name]: undefined } : old));
    },
  });

  const run = async (fn) => {
    if (busy) return undefined;
    setBusy(true);
    setErrors({});
    setMessage(null);
    try {
      return await fn(values);
    } catch (err) {
      const fields = err.fields ?? {};
      setErrors(fields);
      const onField = Object.keys(values).some((k) => fields[k]);
      setMessage(onField ? null : { text: err.message, code: err.code, retryAfter: err.retryAfter });
      return undefined;
    } finally {
      setBusy(false);
    }
  };

  return { values, setValues, message, setMessage, busy, bind, run };
}
