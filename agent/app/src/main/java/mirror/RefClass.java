package mirror;

import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.Modifier;
import java.util.HashMap;

/* loaded from: classes2.dex */
public final class RefClass {
    private static HashMap<Class<?>, Constructor<?>> REF_TYPES;

    static {
        HashMap<Class<?>, Constructor<?>> map = new HashMap<>();
        REF_TYPES = map;
        try {
            map.put(RefObject.class, RefObject.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefMethod.class, RefMethod.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefInt.class, RefInt.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefLong.class, RefLong.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefFloat.class, RefFloat.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefDouble.class, RefDouble.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefBoolean.class, RefBoolean.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefStaticObject.class, RefStaticObject.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefStaticInt.class, RefStaticInt.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefStaticMethod.class, RefStaticMethod.class.getConstructor(Class.class, Field.class));
            REF_TYPES.put(RefConstructor.class, RefConstructor.class.getConstructor(Class.class, Field.class));
        } catch (Exception e) {
            e.printStackTrace();
        }
    }

    public static Class<?> load(Class<?> mappingClass, String className) {
        try {
            return load(mappingClass, Class.forName(className));
        } catch (Exception e) {
            return null;
        }
    }

    public static Class load(Class mappingClass, Class<?> realClass) {
        Constructor<?> constructor;
        Field[] fields = mappingClass.getDeclaredFields();
        for (Field field : fields) {
            try {
                if (Modifier.isStatic(field.getModifiers()) && (constructor = REF_TYPES.get(field.getType())) != null) {
                    field.set(null, constructor.newInstance(realClass, field));
                }
            } catch (Exception e) {
            }
        }
        return realClass;
    }
}
