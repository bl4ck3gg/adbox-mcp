package mirror;

import java.lang.reflect.Field;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;

/* loaded from: classes2.dex */
public class RefMethod<T> {
    private Method method;

    public RefMethod(Class<?> cls, Field field) throws NoSuchMethodException, SecurityException, ClassNotFoundException {
        if (field.isAnnotationPresent(MethodParams.class)) {
            Class<?>[] types = ((MethodParams) field.getAnnotation(MethodParams.class)).value();
            for (int i = 0; i < types.length; i++) {
                Class<?> clazz = types[i];
                if (clazz.getClassLoader() == getClass().getClassLoader()) {
                    try {
                        Class.forName(clazz.getName());
                        Class<?> realClass = (Class) clazz.getField("TYPE").get(null);
                        types[i] = realClass;
                    } catch (Throwable e) {
                        throw new RuntimeException(e);
                    }
                }
            }
            Method declaredMethod = cls.getDeclaredMethod(field.getName(), types);
            this.method = declaredMethod;
            declaredMethod.setAccessible(true);
        } else if (field.isAnnotationPresent(MethodReflectParams.class)) {
            String[] typeNames = ((MethodReflectParams) field.getAnnotation(MethodReflectParams.class)).value();
            Class<?>[] types2 = new Class[typeNames.length];
            for (int i2 = 0; i2 < typeNames.length; i2++) {
                Class<?> type = RefStaticMethod.getProtoType(typeNames[i2]);
                if (type == null) {
                    try {
                        type = Class.forName(typeNames[i2]);
                    } catch (ClassNotFoundException e2) {
                        e2.printStackTrace();
                    }
                }
                types2[i2] = type;
            }
            Method declaredMethod2 = cls.getDeclaredMethod(field.getName(), types2);
            this.method = declaredMethod2;
            declaredMethod2.setAccessible(true);
        } else {
            Method[] declaredMethods = cls.getDeclaredMethods();
            int length = declaredMethods.length;
            int i3 = 0;
            while (true) {
                if (i3 >= length) {
                    break;
                }
                Method method = declaredMethods[i3];
                if (!method.getName().equals(field.getName())) {
                    i3++;
                } else {
                    this.method = method;
                    method.setAccessible(true);
                    break;
                }
            }
        }
        if (this.method == null) {
            throw new NoSuchMethodException(field.getName());
        }
    }

    public T call(Object obj, Object... objArr) {
        try {
            return (T) this.method.invoke(obj, objArr);
        } catch (InvocationTargetException e) {
            if (e.getCause() != null) {
                e.getCause().printStackTrace();
                return null;
            }
            e.printStackTrace();
            return null;
        } catch (Throwable th) {
            th.printStackTrace();
            return null;
        }
    }

    public T callWithException(Object obj, Object... objArr) throws Throwable {
        try {
            return (T) this.method.invoke(obj, objArr);
        } catch (InvocationTargetException e) {
            if (e.getCause() != null) {
                throw e.getCause();
            }
            throw e;
        }
    }

    public Class<?>[] paramList() {
        return this.method.getParameterTypes();
    }
}
